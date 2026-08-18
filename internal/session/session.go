// Package session implements the attach/detach/current continuity primitive.
// Attaching binds a caller (identified by a session id) to a task and records
// it both in the task's append-only sessions.jsonl audit and in a per-session
// "current" pointer so later scoped commands can default to the attached task.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

// Now is the clock for timestamps; overridable in tests.
var Now = func() time.Time { return time.Now().UTC() }

// GlobalSession is the pointer name used when no session id is supplied.
const GlobalSession = "_global"

// Entry is one append-only session association record on a task.
type Entry struct {
	Action  string `json:"action"`
	Session string `json:"session"`
	Actor   string `json:"actor,omitempty"`
	At      string `json:"at"`
}

func pointerFile(ws *workspace.Workspace, sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, sessionID)
	if safe == "" {
		safe = GlobalSession
	}
	return filepath.Join(ws.Path(".sessions"), safe+".current")
}

// Resolve returns the effective session id: the supplied value, else
// $DOCKET_SESSION, else the global pointer name.
func Resolve(supplied, envSession string) string {
	if supplied != "" {
		return supplied
	}
	if envSession != "" {
		return envSession
	}
	return GlobalSession
}

// Attach binds sessionID to taskID, appends to the task audit log, writes the
// current pointer, and returns the task. The event log entry is the caller's
// responsibility (so it can carry the assignee for inbox routing).
func Attach(ws *workspace.Workspace, taskID, sessionID, actor string) (*task.Task, error) {
	t, err := task.Load(ws, taskID)
	if err != nil {
		return nil, err
	}
	pointer := pointerFile(ws, sessionID)
	err = store.WithLock(pointer+".lock", func() error {
		previousID := Current(ws, sessionID)
		if previousID != "" && previousID != t.ID {
			previous, err := task.Load(ws, previousID)
			if err != nil {
				return err
			}
			if err := appendEntry(previous, Entry{
				Action: "detach", Session: sessionID, Actor: actor, At: Now().Format(time.RFC3339Nano),
			}); err != nil {
				return err
			}
			if err := removeFile(pointer); err != nil {
				return err
			}
		}

		attached := Entry{Action: "attach", Session: sessionID, Actor: actor, At: Now().Format(time.RFC3339Nano)}
		if previousID == t.ID {
			return appendEntry(t, attached)
		}
		if err := appendEntry(t, attached); err != nil {
			return err
		}
		if err := store.WriteAtomic(pointer, []byte(t.ID+"\n"), 0o644); err != nil {
			compensation := appendEntry(t, Entry{
				Action: "detach", Session: sessionID, Actor: actor, At: Now().Format(time.RFC3339Nano),
			})
			return errors.Join(err, compensation)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// Detach clears the current pointer for sessionID and records it in the task
// audit log. Returns the task id that was detached (empty if none).
func Detach(ws *workspace.Workspace, sessionID, actor string) (string, error) {
	pointer := pointerFile(ws, sessionID)
	var taskID string
	err := store.WithLock(pointer+".lock", func() error {
		taskID = Current(ws, sessionID)
		if taskID == "" {
			return nil
		}
		t, err := task.Load(ws, taskID)
		if err != nil {
			return err
		}
		if err := appendEntry(t, Entry{
			Action: "detach", Session: sessionID, Actor: actor, At: Now().Format(time.RFC3339Nano),
		}); err != nil {
			return err
		}
		return removeFile(pointer)
	})
	return taskID, err
}

func appendEntry(value *task.Task, entry Entry) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return store.AppendLine(value.SessionsFile(), line)
}

// Current returns the task id bound to sessionID, or "".
func Current(ws *workspace.Workspace, sessionID string) string {
	data, err := readFile(pointerFile(ws, sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ActiveEntries reduces an append-only attach/detach history to the currently
// attached sessions. The latest attach record supplies actor and start time.
func ActiveEntries(entries []Entry) []Entry {
	active := map[string]Entry{}
	for _, entry := range entries {
		switch entry.Action {
		case "attach":
			active[entry.Session] = entry
		case "detach":
			delete(active, entry.Session)
		}
	}
	result := make([]Entry, 0, len(active))
	for _, entry := range active {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		leftTime, leftErr := time.Parse(time.RFC3339Nano, result[left].At)
		rightTime, rightErr := time.Parse(time.RFC3339Nano, result[right].At)
		if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if result[left].At != result[right].At {
			return result[left].At < result[right].At
		}
		return result[left].Session < result[right].Session
	})
	return result
}

// Entries returns the valid session audit records for a task in append order.
func Entries(value *task.Task) ([]Entry, error) {
	file, err := os.Open(value.SessionsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return []Entry{}, nil
		}
		return nil, err
	}
	defer file.Close()
	entries := []Entry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
			entries = append(entries, entry)
		}
	}
	return entries, scanner.Err()
}
