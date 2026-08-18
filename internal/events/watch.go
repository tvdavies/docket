package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/tvdavies/docket/internal/workspace"
)

// Watch streams events as they are appended, calling handler for each. If
// fromStart is true, existing events are replayed first. It blocks until the
// context channel `done` is closed or a fatal error occurs. This is the
// push-based consumer path: a harness reacts to state changes without polling.
func Watch(ws *workspace.Workspace, fromStart bool, done <-chan struct{}, handler func(Event) error) error {
	return WatchWithSetup(ws, fromStart, done, nil, handler)
}

// WatchWithSetup is Watch with a callback run after fsnotify is armed and the
// initial byte offset is captured, but before queued events are drained. A
// service uses setup to drain handler backlog without a race: any event appended
// during setup is still observed from the captured offset afterwards.
func WatchWithSetup(ws *workspace.Workspace, fromStart bool, done <-chan struct{}, setup func() error, handler func(Event) error) error {
	path := ws.EventsFile()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	// Watch the directory so we notice the file being created.
	if err := w.Add(ws.Root); err != nil {
		return err
	}

	var offset int64
	var identity os.FileInfo
	if info, err := os.Stat(path); err == nil {
		identity = info
		if !fromStart {
			offset = info.Size()
		}
	}

	if setup != nil {
		if err := setup(); err != nil {
			return err
		}
	}

	drain := func() error {
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		defer f.Close()
		if info, err := f.Stat(); err == nil {
			if (identity != nil && !os.SameFile(identity, info)) || info.Size() < offset {
				// The log was truncated or replaced. Replay the new file; durable
				// consumers validate their own prefix checkpoints as well.
				offset = 0
			}
			identity = info
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		r := bufio.NewReader(f)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 && line[len(line)-1] == '\n' {
				offset += int64(len(line))
				var ev Event
				if json.Unmarshal(line[:len(line)-1], &ev) == nil {
					if herr := handler(ev); herr != nil {
						return herr
					}
				}
			}
			if err != nil {
				// Partial trailing line (no newline yet): leave offset before it.
				break
			}
		}
		return nil
	}

	if err := drain(); err != nil {
		return err
	}

	for {
		select {
		case <-done:
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			cleanName := filepath.Clean(ev.Name)
			if cleanName == filepath.Clean(ws.Root) && ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				return fmt.Errorf("workspace directory changed: %s", ws.Root)
			}
			if cleanName == filepath.Clean(ws.Path("config.yaml")) {
				if setup != nil {
					if err := setup(); err != nil {
						return err
					}
				}
				continue
			}
			if cleanName == filepath.Clean(path) {
				if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					return fmt.Errorf("event log changed: %s", path)
				}
				// Let durable consumers validate and drain their cursor before
				// streaming individual lines. This also catches in-place rewrites.
				if setup != nil {
					if err := setup(); err != nil {
						return err
					}
				}
				if err := drain(); err != nil {
					return err
				}
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return err
		}
	}
}
