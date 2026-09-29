package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

// Supervision timings are variables so tests can shorten them.
var (
	serviceStopGrace      = 5 * time.Second
	serviceInitialBackoff = time.Second
	serviceHealthInterval = 10 * time.Second
	serviceHealthFailures = 3
	serviceStableAfter    = 10 * time.Second
	serviceMaxBackoff     = 30 * time.Second
	serviceWatchDebounce  = 300 * time.Millisecond
	serviceLogMaxBytes    = int64(5 << 20)
)

// ServiceStatus is the supervisor's view of one plugin service process.
type ServiceStatus struct {
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
	Restarts  int    `json:"restarts"`
	StartedAt string `json:"started_at,omitempty"`
	LastError string `json:"last_error,omitempty"`
	Log       string `json:"log"`
}

// PluginLogPath is where a supervised plugin service's stdout and stderr go.
// DOCKET_STATE_DIR overrides the XDG state directory.
func PluginLogPath(name string) (string, error) {
	base := os.Getenv("DOCKET_STATE_DIR")
	if base == "" {
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			state = filepath.Join(home, ".local", "state")
		}
		base = filepath.Join(state, "docket")
	}
	return filepath.Join(base, "plugins", name, "service.log"), nil
}

type serviceSpec struct {
	name     string
	root     string
	service  plugin.Service
	instance map[string]any
}

// key changes whenever the process must be replaced.
func (spec serviceSpec) key() string {
	encoded, _ := json.Marshal(struct {
		Root     string
		Service  plugin.Service
		Instance map[string]any
	}{spec.root, spec.service, spec.instance})
	return string(encoded)
}

// serviceSpecs lists the service.command processes that should run: one per
// installed plugin enabled in at least one of workspaces. Plugins that cannot
// be loaded or configured are reported in problems and not started.
func serviceSpecs(config *registry.Config, workspaces []registry.WorkspaceEntry) ([]serviceSpec, map[string]string) {
	enabled := map[string]bool{}
	for _, entry := range workspaces {
		declared, err := workspace.LoadDeclaredRoot(entry.Path)
		if err != nil {
			continue
		}
		for name := range declared.Plugins.Values {
			enabled[name] = true
		}
	}
	var specs []serviceSpec
	problems := map[string]string{}
	for _, entry := range config.Plugins {
		if !enabled[entry.Name] {
			continue
		}
		manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
		if err != nil {
			problems[entry.Name] = err.Error()
			continue
		}
		if manifest.Service == nil || len(manifest.Service.Command) == 0 {
			continue
		}
		instance, err := manifest.ResolveInstanceConfig(entry.Config)
		if err != nil {
			problems[entry.Name] = err.Error()
			continue
		}
		specs = append(specs, serviceSpec{name: manifest.Name, root: manifest.Root, service: *manifest.Service, instance: instance})
	}
	return specs, problems
}

// supervisor runs service.command for enabled plugins: it restarts crashed
// processes with backoff, restarts unhealthy ones when service.healthz keeps
// failing, and restarts on service.watch matches.
type supervisor struct {
	ctx      context.Context
	onChange func()

	mu       sync.Mutex
	services map[string]*supervised
	stopped  bool
	wg       sync.WaitGroup
}

func newSupervisor(ctx context.Context, onChange func()) *supervisor {
	return &supervisor{ctx: ctx, onChange: onChange, services: map[string]*supervised{}}
}

// sync starts, replaces and stops processes so exactly specs are running.
func (s *supervisor) sync(specs []serviceSpec) {
	wanted := make(map[string]serviceSpec, len(specs))
	for _, spec := range specs {
		wanted[spec.name] = spec
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	var retired []*supervised
	for name, running := range s.services {
		spec, keep := wanted[name]
		if keep && spec.key() == running.spec.key() {
			delete(wanted, name)
			continue
		}
		retired = append(retired, running)
		delete(s.services, name)
	}
	for name, spec := range wanted {
		ctx, cancel := context.WithCancel(s.ctx)
		running := &supervised{spec: spec, cancel: cancel, done: make(chan struct{}), restart: make(chan string, 1), onChange: s.onChange}
		running.logPath, _ = PluginLogPath(name)
		running.status = ServiceStatus{State: "starting", Log: running.logPath}
		s.services[name] = running
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			running.run(ctx)
		}()
	}
	s.mu.Unlock()
	for _, running := range retired {
		running.cancel()
		<-running.done
	}
	if len(retired) > 0 || len(specs) > 0 {
		s.onChange()
	}
}

func (s *supervisor) statuses() map[string]ServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]ServiceStatus, len(s.services))
	for name, running := range s.services {
		running.mu.Lock()
		result[name] = running.status
		running.mu.Unlock()
	}
	return result
}

func (s *supervisor) stop() {
	s.mu.Lock()
	s.stopped = true
	for name, running := range s.services {
		running.cancel()
		delete(s.services, name)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

type supervised struct {
	spec     serviceSpec
	logPath  string
	cancel   context.CancelFunc
	done     chan struct{}
	restart  chan string
	onChange func()

	mu     sync.Mutex
	status ServiceStatus
}

func (p *supervised) update(fn func(*ServiceStatus)) {
	p.mu.Lock()
	before := p.status
	fn(&p.status)
	changed := before != p.status
	p.mu.Unlock()
	if changed {
		p.onChange()
	}
}

func (p *supervised) run(ctx context.Context) {
	defer close(p.done)
	stopWatch := p.watch(ctx)
	defer stopWatch()
	backoff := serviceInitialBackoff
	for {
		started := time.Now()
		reason := p.runOnce(ctx)
		if ctx.Err() != nil {
			p.update(func(status *ServiceStatus) { status.State, status.PID = "stopped", 0 })
			return
		}
		delay := backoff
		if strings.HasPrefix(reason, "watch:") {
			delay, backoff = 0, serviceInitialBackoff
		} else if time.Since(started) >= serviceStableAfter {
			delay, backoff = serviceInitialBackoff, serviceInitialBackoff
		} else {
			backoff = min(backoff*2, serviceMaxBackoff)
		}
		p.update(func(status *ServiceStatus) {
			status.State, status.PID, status.LastError = "backoff", 0, reason
			status.Restarts++
		})
		p.logf("docket: service %s; restarting in %s", reason, delay)
		if !wait(ctx, delay) {
			p.update(func(status *ServiceStatus) { status.State = "stopped" })
			return
		}
	}
}

// runOnce starts the command and returns why it ended.
func (p *supervised) runOnce(ctx context.Context) string {
	log, err := openServiceLog(p.logPath)
	if err != nil {
		return "log: " + err.Error()
	}
	defer log.Close()
	command, err := p.command(log)
	if err != nil {
		fmt.Fprintf(log, "docket: %v\n", err)
		return err.Error()
	}
	// A watch event that arrived during backoff is satisfied by this start.
	select {
	case <-p.restart:
	default:
	}
	if err := command.Start(); err != nil {
		fmt.Fprintf(log, "docket: start: %v\n", err)
		return "start: " + err.Error()
	}
	fmt.Fprintf(log, "docket: started %s (pid %d)\n", strings.Join(p.spec.service.Command, " "), command.Process.Pid)
	p.update(func(status *ServiceStatus) {
		status.State, status.PID, status.StartedAt = "running", command.Process.Pid, now()
	})
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	health := p.healthChecks(ctx)
	defer health.stop()

	var reason string
	select {
	case err := <-exited:
		if err == nil {
			return "exited"
		}
		return "exited: " + err.Error()
	case <-ctx.Done():
		reason = "stopping"
	case reason = <-p.restart:
	case reason = <-health.failed:
	}
	fmt.Fprintf(log, "docket: %s; stopping pid %d\n", reason, command.Process.Pid)
	terminate(command, exited)
	return reason
}

func (p *supervised) command(log io.Writer) (*exec.Cmd, error) {
	argv := p.spec.service.Command
	program := argv[0]
	if strings.Contains(program, "/") {
		program = filepath.Join(p.spec.root, filepath.FromSlash(program))
	} else if resolved, err := exec.LookPath(program); err == nil {
		program = resolved
	} else {
		return nil, fmt.Errorf("service.command: %w", err)
	}
	command := exec.Command(program, argv[1:]...)
	command.Dir = p.spec.root
	command.Stdout, command.Stderr = log, log
	command.Stdin = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	config, _ := json.Marshal(map[string]any{"config": p.spec.instance})
	environment := map[string]string{
		"DOCKET_PLUGIN": p.spec.name, "DOCKET_PLUGIN_ROOT": p.spec.root,
		"DOCKET_PLUGIN_CONFIG": string(config), "DOCKET_PLUGIN_SERVICE_URL": p.spec.service.URL,
	}
	if target, err := url.Parse(p.spec.service.URL); err == nil && target.Port() != "" {
		environment["PORT"] = target.Port()
	}
	command.Env = mergeEnvironment(os.Environ(), environment)
	return command, nil
}

// terminate stops the whole process group: SIGTERM, then SIGKILL after the
// grace period.
func terminate(command *exec.Cmd, exited <-chan error) {
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(serviceStopGrace):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-exited
	}
}

type healthCheck struct {
	failed chan string
	cancel context.CancelFunc
}

func (h healthCheck) stop() { h.cancel() }

// healthChecks polls service.healthz and reports a failure after
// serviceHealthFailures consecutive misses. The first probe waits one
// interval so a slow start is not a failure.
func (p *supervised) healthChecks(ctx context.Context) healthCheck {
	ctx, cancel := context.WithCancel(ctx)
	check := healthCheck{failed: make(chan string, 1), cancel: cancel}
	if p.spec.service.Healthz == "" {
		return check
	}
	target := strings.TrimRight(p.spec.service.URL, "/") + p.spec.service.Healthz
	client := &http.Client{Timeout: 2 * time.Second}
	go func() {
		misses := 0
		ticker := time.NewTicker(serviceHealthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			problem := ""
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if response, err := client.Do(request); err != nil {
				problem = err.Error()
			} else {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
				response.Body.Close()
				if response.StatusCode >= 300 {
					problem = response.Status
				}
			}
			if ctx.Err() != nil {
				return
			}
			if problem == "" {
				misses = 0
				p.update(func(status *ServiceStatus) {
					if status.State == "running" || status.State == "unhealthy" {
						status.State = "healthy"
					}
				})
				continue
			}
			misses++
			p.update(func(status *ServiceStatus) { status.State, status.LastError = "unhealthy", "healthz: "+problem })
			if misses >= serviceHealthFailures {
				check.failed <- "unhealthy: " + problem
				return
			}
		}
	}()
	return check
}

// watch restarts the process when a file matching service.watch changes.
func (p *supervised) watch(ctx context.Context) func() {
	if len(p.spec.service.Watch) == 0 {
		return func() {}
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		p.logf("docket: service.watch unavailable: %v", err)
		return func() {}
	}
	addTree(watcher, p.spec.root)
	go func() {
		var debounce *time.Timer
		defer func() {
			if debounce != nil {
				debounce.Stop()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						addTree(watcher, event.Name)
					}
				}
				relative, err := filepath.Rel(p.spec.root, event.Name)
				if err != nil || event.Op == fsnotify.Chmod || !p.matches(filepath.ToSlash(relative)) {
					continue
				}
				if debounce != nil {
					debounce.Stop()
				}
				changed := filepath.ToSlash(relative)
				debounce = time.AfterFunc(serviceWatchDebounce, func() {
					select {
					case p.restart <- "watch: " + changed + " changed":
					default:
					}
				})
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return func() { watcher.Close() }
}

func (p *supervised) matches(relative string) bool {
	for _, pattern := range p.spec.service.Watch {
		if plugin.MatchWatch(pattern, relative) {
			return true
		}
	}
	return false
}

func (p *supervised) logf(format string, args ...any) {
	log, err := openServiceLog(p.logPath)
	if err != nil {
		return
	}
	defer log.Close()
	fmt.Fprintf(log, format+"\n", args...)
}

// addTree watches root and its subdirectories, skipping VCS metadata and
// dependency trees that are never useful to restart on.
func addTree(watcher *fsnotify.Watcher, root string) {
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if count++; count > plugin.MaxUIFiles {
			return filepath.SkipAll
		}
		_ = watcher.Add(path)
		return nil
	})
}

// openServiceLog appends to the service log, rotating it to .1 once it
// exceeds serviceLogMaxBytes.
func openServiceLog(path string) (*os.File, error) {
	if path == "" {
		return nil, errors.New("no log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > serviceLogMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func mergeEnvironment(existing []string, values map[string]string) []string {
	result := make([]string, 0, len(existing)+len(values))
	for _, item := range existing {
		if key, _, ok := strings.Cut(item, "="); ok {
			if _, replaced := values[key]; replaced {
				continue
			}
		}
		result = append(result, item)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
