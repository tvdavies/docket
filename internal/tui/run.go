package tui

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
)

// Options configures Run.
type Options struct {
	// WatchDir, when set, is the workspace directory to watch for changes
	// (the event log and config directly inside it, and task and project
	// folders below it) so the board refreshes live.
	WatchDir string
}

// Run opens the board full screen and blocks until the user quits.
func Run(ctx context.Context, src Source, options Options) error {
	program := tea.NewProgram(New(src), tea.WithContext(ctx))
	if options.WatchDir != "" {
		stop := watch(options.WatchDir, func() { program.Send(RefreshMsg{}) })
		defer stop()
	}
	_, err := program.Run()
	return err
}

// watchedTrees are the workspace subdirectories watched recursively: task
// files, comments, and attachments live in per-task folders below tasks/, and
// project names below projects/. Everything else that matters (the event log,
// config) lives directly in the workspace directory.
var watchedTrees = []string{"tasks", "projects"}

// watch calls refresh, debounced, whenever dir or a file in one of its
// watched trees changes. Directories created later (a new task, its first
// comment) are watched as they appear. Failing to watch is not fatal: the
// board still refreshes on r and after its own actions.
func watch(dir string, refresh func()) (stop func()) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return func() {}
	}
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return func() {}
	}
	for _, tree := range watchedTrees {
		addTree(watcher, filepath.Join(dir, tree))
	}
	done := make(chan struct{})
	go func() {
		var timer *time.Timer
		for {
			select {
			case <-done:
				if timer != nil {
					timer.Stop()
				}
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if strings.HasSuffix(event.Name, ".lock") {
					continue
				}
				if event.Has(fsnotify.Create) && inWatchedTree(dir, event.Name) {
					// Walk it: files may have landed before the watch did.
					addTree(watcher, event.Name)
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(150*time.Millisecond, refresh)
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return func() {
		close(done)
		_ = watcher.Close()
	}
}

// addTree watches root and every directory below it. Missing or unreadable
// directories are skipped.
func addTree(watcher *fsnotify.Watcher, root string) {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if entry.IsDir() {
			_ = watcher.Add(path)
		}
		return nil
	})
}

// inWatchedTree reports whether path is one of dir's watched trees or below
// one.
func inWatchedTree(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	for _, tree := range watchedTrees {
		if first == tree {
			return true
		}
	}
	return false
}
