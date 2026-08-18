package events

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

// InboxOptions configures an inbox read.
type InboxOptions struct {
	Actor    string // recipient; events on tasks assigned to this actor
	All      bool   // ignore the assignee filter — every unread event
	MarkRead bool   // advance the cursor past everything read
}

// Inbox returns unread events for an actor since their last cursor. With
// MarkRead, the cursor advances to the end of the log so a later call returns
// only newer events. This is the poll-based consumer path.
func Inbox(ws *workspace.Workspace, opts InboxOptions) ([]Event, error) {
	cursor := Cursor(ws, opts.Actor)
	evs, err := Since(ws, cursor)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, ev := range evs {
		if opts.All || ev.Assignee == opts.Actor {
			out = append(out, ev)
		}
	}
	if opts.MarkRead {
		if err := AdvanceCursor(ws, opts.Actor, Count(ws)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// cursorFile maps an actor id to its cursor path, sanitising the name.
func cursorFile(ws *workspace.Workspace, actor string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, actor)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(ws.CursorsDir(), safe+".cursor")
}

// Cursor returns a consumer's current event-log position. Missing or invalid
// cursor files start at zero, so a newly registered consumer sees history.
func Cursor(ws *workspace.Workspace, actor string) int {
	data, err := os.ReadFile(cursorFile(ws, actor))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

// AdvanceCursor durably records a consumer's event-log position.
func AdvanceCursor(ws *workspace.Workspace, actor string, n int) error {
	return store.WriteAtomic(cursorFile(ws, actor), []byte(strconv.Itoa(n)+"\n"), 0o644)
}
