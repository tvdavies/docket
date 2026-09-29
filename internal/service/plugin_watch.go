package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
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
)

// pluginWatchDebounce coalesces the burst of events an editor save or a
// bundler rebuild produces into one reload.
var pluginWatchDebounce = 150 * time.Millisecond

// PluginState is the instance-level view of one installed plugin published on
// GET /api/stream. ManifestHash changes with any manifest edit; UIHash changes
// with any edit under ui.dir.
type PluginState struct {
	Name         string `json:"name"`
	Version      string `json:"version,omitempty"`
	ManifestHash string `json:"manifest_hash"`
	UIHash       string `json:"ui_hash,omitempty"`
	UIBase       string `json:"ui_base,omitempty"`
	Error        string `json:"error,omitempty"`

	// runtime fingerprints everything that affects workspace runtimes: the
	// registry entry and the manifest without its ui section.
	runtime string
	root    string
	uiDirs  []string
}

type pluginsEvent struct {
	Plugins []PluginState `json:"plugins"`
}

// inspectPlugins reads every installed plugin. It never fails as a whole: a
// broken manifest is reported on its entry so the rest keep hot reloading.
func inspectPlugins(entries []registry.PluginEntry) ([]PluginState, string) {
	states := make([]PluginState, 0, len(entries))
	runtimes := make([]string, 0, len(entries))
	for _, entry := range entries {
		state := inspectPlugin(entry)
		states = append(states, state)
		runtimes = append(runtimes, state.runtime)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	sort.Strings(runtimes)
	return states, strings.Join(runtimes, "|")
}

func inspectPlugin(entry registry.PluginEntry) PluginState {
	state := PluginState{Name: entry.Name, Version: entry.Version, ManifestHash: "missing", root: entry.Path}
	metadata, _ := json.Marshal(entry)
	runtime := "missing"
	if data, err := os.ReadFile(filepath.Join(entry.Path, plugin.ManifestFile)); err == nil {
		state.ManifestHash = shortHash(data)
		runtime = runtimeManifestHash(data)
	}
	state.runtime = fmt.Sprintf("%x:%s", sha256.Sum256(metadata), runtime)

	manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
	if err != nil {
		state.Error = err.Error()
		return state
	}
	state.Version = manifest.Version
	hash, err := manifest.UIHash()
	if err != nil {
		state.Error = err.Error()
	} else if hash != "" {
		state.UIHash = hash
		state.UIBase = "/plugin-ui/" + manifest.Name + "/" + hash
	}
	if dir := manifest.UIDir(); dir != "" {
		state.uiDirs = uiDirectories(dir)
	}
	return state
}

// runtimeManifestHash fingerprints a manifest with its ui section removed, so
// frame, widget and page edits refresh board config without restarting
// handlers. Unparseable manifests hash raw, which forces a restart that
// surfaces the validation error on the workspace.
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

func uiDirectories(root string) []string {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if len(dirs) >= plugin.MaxUIFiles {
				return filepath.SkipAll
			}
			dirs = append(dirs, path)
		}
		return nil
	})
	return dirs
}

func shortHash(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

// pluginHub fans instance-level plugin snapshots out to /api/stream clients.
type pluginHub struct {
	mu          sync.Mutex
	states      []PluginState
	value       string
	set         bool
	closed      bool
	subscribers map[chan []PluginState]struct{}
}

func newPluginHub() *pluginHub {
	return &pluginHub{subscribers: map[chan []PluginState]struct{}{}}
}

// update stores states and reports whether they differ from a previous
// snapshot. The first snapshot is not a change: runtimes read it at start.
func (hub *pluginHub) update(states []PluginState) bool {
	encoded, _ := json.Marshal(states)
	value := string(encoded)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed || hub.value == value {
		return false
	}
	changed := hub.set
	hub.states, hub.value, hub.set = states, value, true
	for channel := range hub.subscribers {
		select {
		case channel <- states:
		default:
			// A slow client only needs the latest snapshot.
			select {
			case <-channel:
			default:
			}
			channel <- states
		}
	}
	return changed
}

func (hub *pluginHub) subscribe() (<-chan []PluginState, []PluginState, func(), bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return nil, nil, func() {}, false
	}
	channel := make(chan []PluginState, 1)
	hub.subscribers[channel] = struct{}{}
	cancel := func() {
		hub.mu.Lock()
		if _, ok := hub.subscribers[channel]; ok {
			delete(hub.subscribers, channel)
			close(channel)
		}
		hub.mu.Unlock()
	}
	return channel, hub.states, cancel, true
}

func (hub *pluginHub) close() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.closed = true
	for channel := range hub.subscribers {
		delete(hub.subscribers, channel)
		close(channel)
	}
}

// pluginWatcher arms fsnotify on each plugin root (for its manifest) and on
// every directory under its ui.dir. It only speeds reloads up: the registry
// poll remains authoritative, so a failed watch is never an error.
type pluginWatcher struct {
	watcher  *fsnotify.Watcher
	ui       map[string]bool
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
	return &pluginWatcher{watcher: watcher, ui: map[string]bool{}, watched: map[string]bool{}, trigger: make(chan struct{}, 1)}
}

func (w *pluginWatcher) sync(states []PluginState) {
	if w == nil {
		return
	}
	wanted := map[string]bool{}
	ui := map[string]bool{}
	for _, state := range states {
		if state.root != "" {
			wanted[state.root] = true
		}
		for _, dir := range state.uiDirs {
			wanted[dir] = true
			ui[dir] = true
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
	w.ui = ui
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
			if w.relevant(event) {
				w.schedule()
			}
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// relevant ignores churn in a plugin root other than its manifest, such as
// service logs, which would otherwise reload constantly. Directory events
// still count so a newly created ui.dir or subdirectory gets watched.
func (w *pluginWatcher) relevant(event fsnotify.Event) bool {
	if event.Op == fsnotify.Chmod {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ui[filepath.Dir(event.Name)] || w.watched[event.Name] {
		return true
	}
	if filepath.Base(event.Name) == plugin.ManifestFile {
		return true
	}
	info, err := os.Stat(event.Name)
	return err == nil && info.IsDir()
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

// serveInstanceStream is GET /api/stream: instance-wide events that are not
// scoped to one workspace. It sends the current plugins snapshot first and a
// fresh one whenever a manifest or ui.dir changes.
func serveInstanceStream(writer http.ResponseWriter, request *http.Request, manager *Manager) {
	if _, ok := writer.(http.Flusher); !ok {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	channel, current, unsubscribe, ok := manager.plugins.subscribe()
	if !ok {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "service is stopping"})
		return
	}
	defer unsubscribe()
	if current == nil {
		// Serving without registry follow: report the installed set once.
		if config, err := registry.Load(); err == nil {
			current, _ = inspectPlugins(config.Plugins)
		}
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	if err := writeStreamFrame(writer, func() error {
		_, err := fmt.Fprint(writer, "retry: 1000\n\n")
		return err
	}); err != nil {
		return
	}
	if err := writeSSE(writer, "plugins", "", pluginsEvent{Plugins: nonNilPlugins(current)}); err != nil {
		return
	}
	heartbeat := time.NewTicker(streamHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case states, open := <-channel:
			if !open {
				return
			}
			if err := writeSSE(writer, "plugins", "", pluginsEvent{Plugins: nonNilPlugins(states)}); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeStreamFrame(writer, func() error {
				_, err := fmt.Fprint(writer, ": ping\n\n")
				return err
			}); err != nil {
				return
			}
		}
	}
}

func nonNilPlugins(states []PluginState) []PluginState {
	if states == nil {
		return []PluginState{}
	}
	return states
}
