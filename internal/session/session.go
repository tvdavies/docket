// Package session implements the attach/detach/current continuity primitive.
// Attaching binds a caller (identified by a session id) to a task and records
// it both in the task's append-only sessions.jsonl audit and in a per-session
// "current" pointer so later scoped commands can default to the attached task.
package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/tvdavies/tadu/internal/store"
	"github.com/tvdavies/tadu/internal/task"
	"github.com/tvdavies/tadu/internal/workspace"
)

// Now is the clock for timestamps; overridable in tests.
var Now = func() time.Time { return time.Now().UTC() }

// GlobalSession is the pointer name used when no session id is supplied.
const GlobalSession = "_global"

// entry is one line in a task's sessions.jsonl.
type entry struct {
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
// $TADU_SESSION, else the global pointer name.
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
	e := entry{Action: "attach", Session: sessionID, Actor: actor, At: Now().Format(time.RFC3339)}
	line, _ := json.Marshal(e)
	if err := store.AppendLine(t.SessionsFile(), line); err != nil {
		return nil, err
	}
	if err := store.WriteAtomic(pointerFile(ws, sessionID), []byte(t.ID+"\n"), 0o644); err != nil {
		return nil, err
	}
	return t, nil
}

// Detach clears the current pointer for sessionID and records it in the task
// audit log. Returns the task id that was detached (empty if none).
func Detach(ws *workspace.Workspace, sessionID, actor string) (string, error) {
	taskID := Current(ws, sessionID)
	if taskID == "" {
		return "", nil
	}
	if t, err := task.Load(ws, taskID); err == nil {
		e := entry{Action: "detach", Session: sessionID, Actor: actor, At: Now().Format(time.RFC3339)}
		line, _ := json.Marshal(e)
		_ = store.AppendLine(t.SessionsFile(), line)
	}
	_ = removeFile(pointerFile(ws, sessionID))
	return taskID, nil
}

// Current returns the task id bound to sessionID, or "".
func Current(ws *workspace.Workspace, sessionID string) string {
	data, err := readFile(pointerFile(ws, sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
