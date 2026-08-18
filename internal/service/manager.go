// Package service runs Docket's machine-wide multi-workspace runtime and HTTP
// board/API surface. Workspaces remain independent stores; this package only
// coordinates their watchers in one user process.
package service

import (
	"context"
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
	entry  registry.WorkspaceEntry
	cancel context.CancelFunc

	mu     sync.RWMutex
	status WorkspaceStatus
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
	wanted := make(map[string]registry.WorkspaceEntry, len(entries))
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
		if keep && entry.Path == running.entry.Path {
			delete(wanted, name)
			continue
		}
		running.cancel()
		delete(m.runtimes, name)
	}
	for name, entry := range wanted {
		ctx, cancel := context.WithCancel(m.ctx)
		running := &runtime{
			entry:  entry,
			cancel: cancel,
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
// workspace remains event-driven.
func (m *Manager) FollowRegistry(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	load := func() {
		config, err := registry.Load()
		if err != nil {
			fmt.Fprintf(m.output, "docket: service registry: %v\n", err)
			return
		}
		m.SetWorkspaces(config.Workspaces)
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
		running.cancel()
		delete(m.runtimes, name)
	}
	m.mu.Unlock()
	m.wg.Wait()
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
		drain := func() error {
			fresh, err := workspace.OpenRoot(running.entry.Path)
			if err != nil {
				return err
			}
			failures := handlers.DrainAll(fresh, handlers.Options{Context: ctx, Scope: handlers.ScopeAll, Output: m.output})
			running.update(func(status *WorkspaceStatus) {
				status.EventCount = events.Count(fresh)
				status.HandlerCount = len(fresh.Config.Handlers)
				status.UpdatedAt = now()
			})
			if len(failures) == 0 {
				return nil
			}
			errs := make([]error, 0, len(failures))
			for _, failure := range failures {
				errs = append(errs, failure)
			}
			return errors.Join(errs...)
		}

		err = events.WatchWithSetup(ws, false, done, func() error {
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
		}, func(event events.Event) error {
			running.update(func(status *WorkspaceStatus) {
				status.LastEvent = event.Time
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
