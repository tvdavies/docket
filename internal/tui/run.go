package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
)

// Options configures Run.
type Options struct {
	// WatchDir, when set, is watched for changes (the event log and config
	// live directly inside it) so the board refreshes live.
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

// watch calls refresh, debounced, whenever dir changes. Failing to watch is
// not fatal: the board still refreshes on r and after its own actions.
func watch(dir string, refresh func()) (stop func()) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return func() {}
	}
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return func() {}
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
