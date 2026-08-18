package events

import (
	"bufio"
	"encoding/json"
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
	if !fromStart {
		if info, err := os.Stat(path); err == nil {
			offset = info.Size()
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
			if filepath.Clean(ev.Name) == filepath.Clean(path) {
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
