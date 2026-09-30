// Package service runs Docket's headless event runner. It watches registered
// workspaces and drains their durable handler cursors. Workspaces remain
// independent stores; this package only coordinates their watchers in one
// user process. It opens no network listener.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

const maxRetry = 30 * time.Second

// WorkspaceStatus is the runner's live view of one registered workspace.
type WorkspaceStatus struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	State        string `json:"state"`
	EventCount   int    `json:"event_count"`
	HandlerCount int    `json:"handler_count"`
	LastEvent    string `json:"last_event,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	UpdatedAt    string `json:"updated_at"`
}

type runtime struct {
	entry      registry.WorkspaceEntry
	generation string
	cancel     context.CancelFunc

	mu           sync.RWMutex
	status       WorkspaceStatus
	handlerNames map[string]bool
	initialised  bool
}

// Manager owns one runtime per registered workspace.
type Manager struct {
	ctx    context.Context
	output io.Writer

	mu       sync.RWMutex
	runtimes map[string]*runtime
	stopped  bool
	wg       sync.WaitGroup
}

func NewManager(ctx context.Context, output io.Writer) *Manager {
	if output == nil {
		output = io.Discard
	}
	return &Manager{ctx: ctx, output: output, runtimes: map[string]*runtime{}}
}

// SetWorkspaces reconciles the running set with entries. Unchanged workspaces
// keep running; changed, added, and removed registrations are restarted safely.
func (m *Manager) SetWorkspaces(entries []registry.WorkspaceEntry) {
	m.setWorkspaces(entries, "")
}

func (m *Manager) setWorkspaces(entries []registry.WorkspaceEntry, generation string) {
	wanted := make(map[string]registry.WorkspaceEntry, len(entries))
	inherited := map[string]struct {
		names       map[string]bool
		initialised bool
	}{}
	for _, entry := range entries {
		wanted[entry.Name] = entry
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	for name, running := range m.runtimes {
		entry, keep := wanted[name]
		if keep && entry.Path == running.entry.Path && running.generation == generation {
			delete(wanted, name)
			continue
		}
		running.mu.RLock()
		names := make(map[string]bool, len(running.handlerNames))
		for handler := range running.handlerNames {
			names[handler] = true
		}
		inherited[name] = struct {
			names       map[string]bool
			initialised bool
		}{names: names, initialised: running.initialised}
		running.mu.RUnlock()
		running.cancel()
		delete(m.runtimes, name)
	}
	for name, entry := range wanted {
		ctx, cancel := context.WithCancel(m.ctx)
		prior := inherited[name]
		running := &runtime{
			entry: entry, generation: generation, cancel: cancel,
			handlerNames: prior.names, initialised: prior.initialised,
			status: WorkspaceStatus{
				Name: entry.Name, Path: entry.Path, State: "starting", UpdatedAt: now(),
			},
		}
		m.runtimes[name] = running
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.runWorkspace(ctx, running)
		}()
	}
}

// FollowRegistry reloads the machine-local registry periodically. The interval
// is deliberately small and cheap: only config metadata is read, while each
// workspace remains event-driven. Registrations whose project directories stay
// missing beyond the configured prune_after grace are unregistered so dead
// paths do not accumulate retrying watchers. Plugin manifest edits are also
// watched with fsnotify so they apply without waiting for the next poll;
// removing or relocating a plugin is picked up by the poll.
func (m *Manager) FollowRegistry(ctx context.Context, interval time.Duration) {
	missing := map[string]time.Time{}
	m.followPlugins(ctx, interval, func(config *registry.Config) []registry.WorkspaceEntry {
		return registry.PruneMissing(config, missing, time.Now(), func(format string, args ...any) {
			fmt.Fprintf(m.output, format+"\n", args...)
		})
	})
}

// WatchPlugins hot reloads installed plugins for the workspaces already set
// with SetWorkspaces, without following registry workspace changes.
func (m *Manager) WatchPlugins(ctx context.Context, interval time.Duration) {
	m.followPlugins(ctx, interval, func(*registry.Config) []registry.WorkspaceEntry {
		m.mu.RLock()
		defer m.mu.RUnlock()
		entries := make([]registry.WorkspaceEntry, 0, len(m.runtimes))
		for _, running := range m.runtimes {
			entries = append(entries, running.entry)
		}
		return entries
	})
}

// followPlugins reconciles runtimes on every poll tick and plugin manifest
// change. A changed plugin generation restarts the affected runtimes, which
// recompose their handlers from the new manifests.
func (m *Manager) followPlugins(ctx context.Context, interval time.Duration, workspaces func(*registry.Config) []registry.WorkspaceEntry) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	watcher := newPluginWatcher()
	go watcher.run(ctx.Done())
	reported := map[string]string{}
	load := func() {
		config, err := registry.Load()
		if err != nil {
			fmt.Fprintf(m.output, "docket: runner registry: %v\n", err)
			return
		}
		states, generation := inspectPlugins(config.Plugins)
		watcher.sync(states)
		entries := workspaces(config)
		m.setWorkspaces(entries, generation)
		problems := hostingProblems(config, entries)
		for name, problem := range problems {
			if reported[name] != problem {
				fmt.Fprintf(m.output, "docket: plugin %s: %s\n", name, problem)
			}
		}
		reported = problems
	}
	load()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			load()
		case <-watcher.events():
			load()
		}
	}
}

// Statuses returns a stable snapshot ordered by workspace name.
func (m *Manager) Statuses() []WorkspaceStatus {
	m.mu.RLock()
	runtimes := make([]*runtime, 0, len(m.runtimes))
	for _, running := range m.runtimes {
		runtimes = append(runtimes, running)
	}
	m.mu.RUnlock()

	statuses := make([]WorkspaceStatus, 0, len(runtimes))
	for _, running := range runtimes {
		running.mu.RLock()
		statuses = append(statuses, running.status)
		running.mu.RUnlock()
	}
	sortStatuses(statuses)
	return statuses
}

// Stop cancels every workspace and waits for its watcher to leave.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	for name, running := range m.runtimes {
		running.cancel()
		delete(m.runtimes, name)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// drainWorkspace delivers every pending event to the workspace's handlers.
// Plugin handlers that appear after the runtime first drained (a hot-reloaded
// manifest) are seeded at the log end so enabling them never replays history.
func (m *Manager) drainWorkspace(ctx context.Context, running *runtime) error {
	fresh, err := workspace.OpenRoot(running.entry.Path)
	if err != nil {
		return err
	}
	running.mu.RLock()
	initialised := running.initialised
	previous := make(map[string]bool, len(running.handlerNames))
	for name := range running.handlerNames {
		previous[name] = true
	}
	running.mu.RUnlock()
	if initialised {
		for name, config := range fresh.Config.Handlers {
			if config.PluginName != "" && !previous[name] {
				if err := handlers.SeedCursorAtEnd(fresh, name); err != nil {
					return fmt.Errorf("seed hot-reloaded plugin handler %q: %w", name, err)
				}
			}
		}
	}
	failures := handlers.DrainAll(fresh, handlers.Options{Context: ctx, Scope: handlers.ScopeAll, Output: m.output, RefreshConfig: true})
	current := make(map[string]bool, len(fresh.Config.Handlers))
	for name := range fresh.Config.Handlers {
		current[name] = true
	}
	running.mu.Lock()
	running.handlerNames = current
	running.initialised = true
	running.status.EventCount = events.Count(fresh)
	running.status.HandlerCount = len(fresh.Config.Handlers)
	running.status.UpdatedAt = now()
	running.mu.Unlock()
	if len(failures) == 0 {
		return nil
	}
	errs := make([]error, 0, len(failures))
	for _, failure := range failures {
		errs = append(errs, failure)
	}
	return errors.Join(errs...)
}

func (m *Manager) runWorkspace(ctx context.Context, running *runtime) {
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			running.update(func(status *WorkspaceStatus) {
				status.State = "stopped"
				status.UpdatedAt = now()
			})
			return
		}

		ws, err := workspace.OpenRoot(running.entry.Path)
		if err != nil {
			m.fail(running, "unavailable", err)
			if !wait(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		started := false
		drain := func() error { return m.drainWorkspace(ctx, running) }
		// The watcher is armed before setup runs, and setup drains the backlog,
		// so an event appended during startup is either drained here or
		// delivered by the watcher afterwards. Setup also reruns whenever
		// config.yaml changes, applying hook changes without a new event.
		err = events.WatchWithSetupCursor(ws, false, done, func(events.LogCursor, bool) error {
			if err := drain(); err != nil {
				return err
			}
			started = true
			backoff = time.Second
			running.update(func(status *WorkspaceStatus) {
				if status.State == "retrying" || status.State == "unavailable" {
					fmt.Fprintf(m.output, "docket: workspace %s watching again\n", running.entry.Name)
				}
				status.State = "watching"
				status.LastError = ""
				status.UpdatedAt = now()
			})
			return nil
		}, func(record events.LogRecord) error {
			running.update(func(status *WorkspaceStatus) {
				status.LastEvent = record.Event.Time
				status.UpdatedAt = now()
			})
			return drain()
		})
		if ctx.Err() != nil {
			continue
		}
		m.fail(running, "retrying", err)
		if !wait(ctx, backoff) {
			return
		}
		if !started {
			backoff = nextBackoff(backoff)
		}
	}
}

// OnceResult reports one bounded drain of one workspace.
type OnceResult struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	EventCount   int    `json:"event_count"`
	HandlerCount int    `json:"handler_count"`
	// State is "ok", "failed", or "missing" for a registered project
	// directory that no longer exists (see OnceOptions.SkipMissing).
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// OnceOptions configures RunOnce.
type OnceOptions struct {
	// SkipMissing reports a registration whose project directory no longer
	// exists as "missing" instead of failing the run. A registry-wide
	// heartbeat uses this so one deleted project does not fail every run;
	// the long-running runner prunes such registrations after prune_after.
	SkipMissing bool
	// Registry, when set, is checked for enabled plugins that fail to load
	// or declare an unlaunched service.command, reported like the runner.
	Registry *registry.Config
}

// RunOnce performs one bounded drain of every entry and returns. Failed or
// unprocessed events stay pending for the next drain; nothing is rolled
// back. The returned error joins every workspace failure.
func RunOnce(ctx context.Context, entries []registry.WorkspaceEntry, output io.Writer, options OnceOptions) ([]OnceResult, error) {
	manager := NewManager(ctx, output)
	if options.Registry != nil {
		problems := hostingProblems(options.Registry, entries)
		names := make([]string, 0, len(problems))
		for name := range problems {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(manager.output, "docket: plugin %s: %s\n", name, problems[name])
		}
	}
	results := make([]OnceResult, 0, len(entries))
	var failures []error
	sorted := append([]registry.WorkspaceEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, entry := range sorted {
		if options.SkipMissing {
			if _, err := os.Stat(entry.Path); os.IsNotExist(err) {
				fmt.Fprintf(manager.output, "docket: workspace %s missing: %s does not exist\n", entry.Name, entry.Path)
				results = append(results, OnceResult{Name: entry.Name, Path: entry.Path, State: "missing"})
				continue
			}
		}
		running := &runtime{entry: entry, status: WorkspaceStatus{Name: entry.Name, Path: entry.Path}}
		err := manager.drainWorkspace(ctx, running)
		result := OnceResult{Name: entry.Name, Path: entry.Path, EventCount: running.status.EventCount, HandlerCount: running.status.HandlerCount, State: "ok"}
		if err != nil {
			result.State = "failed"
			result.Error = err.Error()
			failures = append(failures, fmt.Errorf("workspace %s: %w", entry.Name, err))
		}
		results = append(results, result)
	}
	return results, errors.Join(failures...)
}

// fail records a workspace failure and reports it on the runner output when
// the state or error changes, so a broken config or plugin manifest that stops
// hook delivery is visible in the runner log rather than only in Statuses.
func (m *Manager) fail(running *runtime, state string, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	changed := false
	running.update(func(status *WorkspaceStatus) {
		changed = status.State != state || status.LastError != message
		status.State = state
		if message != "" {
			status.LastError = message
		}
		status.UpdatedAt = now()
	})
	if changed {
		fmt.Fprintf(m.output, "docket: workspace %s %s: %s\n", running.entry.Name, state, message)
	}
}

func (r *runtime) update(fn func(*WorkspaceStatus)) {
	r.mu.Lock()
	fn(&r.status)
	r.mu.Unlock()
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(current time.Duration) time.Duration {
	current *= 2
	if current > maxRetry {
		return maxRetry
	}
	return current
}

func sortStatuses(statuses []WorkspaceStatus) {
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
