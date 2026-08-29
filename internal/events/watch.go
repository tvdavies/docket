package events

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
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
// context channel done is closed or a fatal error occurs.
func Watch(ws *workspace.Workspace, fromStart bool, done <-chan struct{}, handler func(Event) error) error {
	return WatchWithSetup(ws, fromStart, done, nil, handler)
}

// WatchWithSetup is Watch with a callback run after fsnotify is armed and the
// initial byte offset is captured, but before queued events are drained.
func WatchWithSetup(ws *workspace.Workspace, fromStart bool, done <-chan struct{}, setup func() error, handler func(Event) error) error {
	var cursorSetup func(LogCursor, bool) error
	if setup != nil {
		cursorSetup = func(LogCursor, bool) error { return setup() }
	}
	return WatchWithSetupCursor(ws, fromStart, done, cursorSetup, func(record LogRecord) error {
		return handler(record.Event)
	})
}

// WatchWithSetupCursor exposes the physical byte boundary for setup and every
// valid event. A reset record means the log was truncated or replaced and the
// consumer must discard any state derived from the previous generation.
func WatchWithSetupCursor(ws *workspace.Workspace, fromStart bool, done <-chan struct{}, setup func(LogCursor, bool) error, handler func(LogRecord) error) error {
	path := ws.EventsFile()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(ws.Root); err != nil {
		return err
	}

	var offset int64
	var identity os.FileInfo
	prefix := sha256.New()
	if info, statErr := os.Stat(path); statErr == nil {
		identity = info
		if !fromStart {
			cursor, cursorErr := CurrentLogCursor(ws)
			if cursorErr != nil {
				return cursorErr
			}
			offset = cursor.Offset
			file, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			if _, copyErr := io.CopyN(prefix, file, offset); copyErr != nil {
				_ = file.Close()
				return copyErr
			}
			_ = file.Close()
		}
	}
	currentCursor := func() LogCursor {
		if offset == 0 {
			return LogCursor{}
		}
		return LogCursor{Offset: offset, PrefixHash: hex.EncodeToString(prefix.Sum(nil))}
	}
	if setup != nil {
		if err := setup(currentCursor(), false); err != nil {
			return err
		}
	}

	drain := func() error {
		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		defer file.Close()
		reset := false
		if info, statErr := file.Stat(); statErr == nil {
			if (identity != nil && !os.SameFile(identity, info)) || info.Size() < offset {
				offset = 0
				prefix = sha256.New()
				reset = true
			}
			identity = info
		}
		if !reset && offset > 0 {
			actual := sha256.New()
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if _, err := io.CopyN(actual, file, offset); err != nil {
				return err
			}
			if hex.EncodeToString(actual.Sum(nil)) != hex.EncodeToString(prefix.Sum(nil)) {
				offset = 0
				prefix = sha256.New()
				reset = true
			}
		}
		if reset && setup != nil {
			if err := setup(currentCursor(), true); err != nil {
				return err
			}
			reset = false
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		reader := bufio.NewReader(file)
		for {
			line, readErr := reader.ReadBytes('\n')
			if len(line) > 0 && line[len(line)-1] == '\n' {
				offset += int64(len(line))
				_, _ = prefix.Write(line)
				var event Event
				if json.Unmarshal(line[:len(line)-1], &event) == nil {
					record := LogRecord{Event: event, Offset: offset, PrefixHash: hex.EncodeToString(prefix.Sum(nil)), Reset: reset}
					reset = false
					if err := handler(record); err != nil {
						return err
					}
				}
			}
			if readErr != nil {
				return nil // partial trailing line remains before offset
			}
		}
	}

	if err := drain(); err != nil {
		return err
	}
	for {
		select {
		case <-done:
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			cleanName := filepath.Clean(event.Name)
			if cleanName == filepath.Clean(ws.Root) && event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				return fmt.Errorf("workspace directory changed: %s", ws.Root)
			}
			if cleanName == filepath.Clean(ws.Path("config.yaml")) {
				if setup != nil {
					if err := setup(currentCursor(), false); err != nil {
						return err
					}
				}
				continue
			}
			if cleanName == filepath.Clean(path) {
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					return fmt.Errorf("event log changed: %s", path)
				}
				if err := drain(); err != nil {
					return err
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			return err
		}
	}
}
