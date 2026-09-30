package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

// pluginWatchDebounce coalesces the burst of events an editor save produces
// into one reload.
var pluginWatchDebounce = 150 * time.Millisecond

// pluginState fingerprints one installed plugin for hook composition.
type pluginState struct {
	name string
	root string
	// runtime changes with the registry entry or any manifest edit outside
	// its legacy presentation sections.
	runtime string
}

// inspectPlugins fingerprints every installed plugin. It never fails as a
// whole: an unreadable manifest hashes as missing, and the workspace that
// enables it surfaces the validation error when it next opens.
func inspectPlugins(entries []registry.PluginEntry) ([]pluginState, string) {
	states := make([]pluginState, 0, len(entries))
	runtimes := make([]string, 0, len(entries))
	for _, entry := range entries {
		metadata, _ := json.Marshal(entry)
		runtime := "missing"
		if data, err := os.ReadFile(filepath.Join(entry.Path, plugin.ManifestFile)); err == nil {
			runtime = runtimeManifestHash(data)
		}
		state := pluginState{name: entry.Name, root: entry.Path, runtime: fmt.Sprintf("%x:%s", sha256.Sum256(metadata), runtime)}
		states = append(states, state)
		runtimes = append(runtimes, state.runtime)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].name < states[j].name })
	sort.Strings(runtimes)
	return states, strings.Join(runtimes, "|")
}

// runtimeManifestHash fingerprints a manifest without its legacy ui section,
// which no longer affects anything Docket runs. Unparseable manifests hash
// raw, which forces a restart that surfaces the validation error on the
// workspace.
func runtimeManifestHash(data []byte) string {
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil || document == nil {
		return shortHash(data)
	}
	delete(document, "ui")
	encoded, err := json.Marshal(document)
	if err != nil {
		return shortHash(data)
	}
	return shortHash(encoded)
}

func shortHash(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

// hostingProblems reports enabled plugins whose manifests fail to load (which
// makes their workspaces unavailable) or expect Docket to launch
// service.command. Docket no longer hosts plugin processes, so the operator
// must run such a command under an OS or container supervisor; hooks, CLI and
// task access keep working and that case is only a warning.
func hostingProblems(config *registry.Config, workspaces []registry.WorkspaceEntry) map[string]string {
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
		if problem := manifest.HostingProblem(); problem != "" {
			problems[entry.Name] = problem
		}
	}
	return problems
}

// pluginWatcher arms fsnotify on each plugin root for its manifest. It only
// speeds reloads up: the registry poll remains authoritative, so a failed
// watch is never an error.
type pluginWatcher struct {
	watcher  *fsnotify.Watcher
	watched  map[string]bool
	trigger  chan struct{}
	debounce *time.Timer
	mu       sync.Mutex
}

func newPluginWatcher() *pluginWatcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	return &pluginWatcher{watcher: watcher, watched: map[string]bool{}, trigger: make(chan struct{}, 1)}
}

func (w *pluginWatcher) sync(states []pluginState) {
	if w == nil {
		return
	}
	wanted := map[string]bool{}
	for _, state := range states {
		if state.root != "" {
			wanted[state.root] = true
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for path := range w.watched {
		if !wanted[path] {
			_ = w.watcher.Remove(path)
			delete(w.watched, path)
		}
	}
	for path := range wanted {
		if !w.watched[path] && w.watcher.Add(path) == nil {
			w.watched[path] = true
		}
	}
}

func (w *pluginWatcher) run(done <-chan struct{}) {
	if w == nil {
		return
	}
	defer w.watcher.Close()
	for {
		select {
		case <-done:
			w.mu.Lock()
			if w.debounce != nil {
				w.debounce.Stop()
			}
			w.mu.Unlock()
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if relevantPluginEvent(event) {
				w.schedule()
			}
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// relevantPluginEvent ignores churn in a plugin root other than its manifest,
// such as logs, which would otherwise reload constantly.
func relevantPluginEvent(event fsnotify.Event) bool {
	return event.Op != fsnotify.Chmod && filepath.Base(event.Name) == plugin.ManifestFile
}

func (w *pluginWatcher) schedule() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.debounce != nil {
		w.debounce.Stop()
	}
	w.debounce = time.AfterFunc(pluginWatchDebounce, func() {
		select {
		case w.trigger <- struct{}{}:
		default:
		}
	})
}

func (w *pluginWatcher) events() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.trigger
}
