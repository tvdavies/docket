// Package service runs Docket's machine-wide multi-workspace runtime and HTTP
// board/API surface. Workspaces remain independent stores; this package only
// coordinates their watchers in one user process.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

const maxRetry = 30 * time.Second

// ErrWorkspaceNotManaged indicates that a URL workspace name is not registered
// with this service manager.
var ErrWorkspaceNotManaged = errors.New("workspace is not managed by this service")

// WorkspaceStatus is the service's live view of one registered workspace.
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
	stream     *workspaceStream

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

	plugins  *pluginHub
	services *supervisor

	pluginMu   sync.Mutex
	pluginBase []PluginState
}

func NewManager(ctx context.Context, output io.Writer) *Manager {
	if output == nil {
		output = io.Discard
	}
	manager := &Manager{ctx: ctx, output: output, runtimes: map[string]*runtime{}, plugins: newPluginHub()}
	manager.services = newSupervisor(ctx, manager.publishPlugins)
	return manager
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
		running.stream.close()
		running.cancel()
		delete(m.runtimes, name)
	}
	for name, entry := range wanted {
		ctx, cancel := context.WithCancel(m.ctx)
		prior := inherited[name]
		running := &runtime{
			entry: entry, generation: generation,
			cancel: cancel, stream: newWorkspaceStream(),
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
// paths do not accumulate retrying watchers. Plugin manifests and ui.dir trees
// are also watched with fsnotify, so plugin edits apply without waiting for
// the next poll.
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

// followPlugins reconciles runtimes on every poll tick and plugin file change.
// Only changes outside a manifest's ui section restart workspace runtimes;
// UI-only changes republish board config to the running streams, which lets
// open plugin frames swap to the new asset generation in place.
func (m *Manager) followPlugins(ctx context.Context, interval time.Duration, workspaces func(*registry.Config) []registry.WorkspaceEntry) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	watcher := newPluginWatcher()
	go watcher.run(ctx.Done())
	previous := ""
	reported := map[string]string{}
	load := func() {
		config, err := registry.Load()
		if err != nil {
			fmt.Fprintf(m.output, "docket: service registry: %v\n", err)
			return
		}
		states, generation := inspectPlugins(config.Plugins)
		watcher.sync(states)
		encoded, _ := json.Marshal(states)
		changed := previous != "" && previous != string(encoded)
		previous = string(encoded)
		m.setPluginStates(states)
		entries := workspaces(config)
		m.setWorkspaces(entries, generation)
		specs, problems := serviceSpecs(config, entries)
		for name, problem := range problems {
			if reported[name] != problem {
				fmt.Fprintf(m.output, "docket: plugin %s service: %s\n", name, problem)
			}
		}
		reported = problems
		m.services.sync(specs)
		if changed {
			m.refreshConfigs()
		}
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

// refreshConfigs republishes board config for every running workspace. The
// stream deduplicates unchanged config, so only affected boards see an event.
func (m *Manager) refreshConfigs() {
	m.mu.RLock()
	runtimes := make([]*runtime, 0, len(m.runtimes))
	for _, running := range m.runtimes {
		runtimes = append(runtimes, running)
	}
	m.mu.RUnlock()
	for _, running := range runtimes {
		ws, err := workspace.OpenRoot(running.entry.Path)
		if err != nil {
			continue
		}
		running.stream.setConfig(configForStream(ws))
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

// LeaseWorkspace opens a fresh authoritative view by registry name and holds a
// manager read lease until release is called. Reconciliation therefore cannot
// replace or remove that name while an HTTP request is reading or mutating its
// store.
func (m *Manager) LeaseWorkspace(name string) (*workspace.Workspace, func(), error) {
	m.mu.RLock()
	running, ok := m.runtimes[name]
	if !ok {
		m.mu.RUnlock()
		return nil, nil, fmt.Errorf("%w: %s", ErrWorkspaceNotManaged, name)
	}
	ws, err := workspace.OpenRoot(running.entry.Path)
	if err != nil {
		m.mu.RUnlock()
		return nil, nil, err
	}
	return ws, m.mu.RUnlock, nil
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
		running.stream.close()
		running.cancel()
		delete(m.runtimes, name)
	}
	m.mu.Unlock()
	m.services.stop()
	m.plugins.close()
	m.wg.Wait()
}

// setPluginStates records the inspected plugin set and publishes it with the
// current service status attached.
func (m *Manager) setPluginStates(states []PluginState) {
	m.pluginMu.Lock()
	m.pluginBase = states
	m.pluginMu.Unlock()
	m.publishPlugins()
}

// publishPlugins pushes the plugin set to /api/stream. The supervisor calls
// it whenever a service changes state.
func (m *Manager) publishPlugins() {
	m.pluginMu.Lock()
	defer m.pluginMu.Unlock()
	if m.pluginBase == nil {
		return
	}
	statuses := m.services.statuses()
	states := make([]PluginState, len(m.pluginBase))
	copy(states, m.pluginBase)
	for index := range states {
		if status, ok := statuses[states[index].Name]; ok {
			states[index].Service = &status
		}
	}
	m.plugins.update(states)
}

// ServiceStatuses reports supervised plugin services by plugin name.
func (m *Manager) ServiceStatuses() map[string]ServiceStatus {
	return m.services.statuses()
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
			running.fail("unavailable", err)
			if !wait(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		started := false
		running.stream.restart()
		drain := func() error {
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

		err = events.WatchWithSetupCursor(ws, false, done, func(cursor events.LogCursor, reset bool) error {
			fresh, err := workspace.OpenRoot(running.entry.Path)
			if err != nil {
				return err
			}
			running.stream.observe(cursor, reset)
			running.stream.setConfig(configForStream(fresh))
			if err := drain(); err != nil {
				return err
			}
			started = true
			backoff = time.Second
			running.update(func(status *WorkspaceStatus) {
				status.State = "watching"
				status.LastError = ""
				status.UpdatedAt = now()
			})
			return nil
		}, func(record events.LogRecord) error {
			if err := m.publishTaskEvent(running, record); err != nil {
				return err
			}
			running.update(func(status *WorkspaceStatus) {
				status.LastEvent = record.Event.Time
				status.UpdatedAt = now()
			})
			return drain()
		})
		if ctx.Err() != nil {
			continue
		}
		running.fail("retrying", err)
		if !wait(ctx, backoff) {
			return
		}
		if !started {
			backoff = nextBackoff(backoff)
		}
	}
}

func (r *runtime) fail(state string, err error) {
	r.update(func(status *WorkspaceStatus) {
		status.State = state
		if err != nil {
			status.LastError = err.Error()
		}
		status.UpdatedAt = now()
	})
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
