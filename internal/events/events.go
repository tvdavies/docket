// Package events implements docket's append-only event log (events.jsonl) — the
// coordination primitive. Every mutation appends one JSON line. Consumers
// either poll a filtered view (the "inbox") or stream new lines (`docket watch`).
// The store never executes anything; it only records.
package events

import (
	"bufio"
	"encoding/json"
	"os"
	"time"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

// Event types.
const (
	TaskCreated   = "task.created"
	TaskUpdated   = "task.updated"
	TaskMoved     = "task.moved"
	TaskCommented = "task.commented"
	TaskLabeled   = "task.labeled"
	TaskLinked    = "task.linked"
	TaskUnlinked  = "task.unlinked"
	TaskAttached  = "task.attached"
	TaskDetached  = "task.detached"
	FileAttached  = "task.file_attached"
	TaskAssigned  = "task.assigned"
)

// Event is one line in the log. Assignee is denormalised so an inbox query can
// filter by recipient without loading every task.
type Event struct {
	Seq      int            `json:"seq"`
	Time     string         `json:"time"`
	Type     string         `json:"type"`
	Task     string         `json:"task,omitempty"`
	Title    string         `json:"title,omitempty"`
	Actor    string         `json:"actor,omitempty"`
	Assignee string         `json:"assignee,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// Now is the clock for event timestamps; overridable in tests.
var Now = func() time.Time { return time.Now().UTC() }

// Append writes one event to the log. Seq and Time are filled in if unset.
// Distinct single-line O_APPEND writes are atomic, so this needs no lock.
func Append(ws *workspace.Workspace, ev Event) error {
	if ev.Time == "" {
		ev.Time = Now().Format(time.RFC3339Nano)
	}
	if ev.Seq == 0 {
		ev.Seq = nextSeq(ws)
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return store.AppendLine(ws.EventsFile(), line)
}

// nextSeq derives a sequence number from the current line count. It is
// advisory (used for display/cursoring); correctness relies on the log being
// append-only, not on perfectly gapless sequencing under heavy concurrency.
func nextSeq(ws *workspace.Workspace) int {
	return countLines(ws.EventsFile()) + 1
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			n++
		}
	}
	return n
}

// All reads every event in order.
func All(ws *workspace.Workspace) ([]Event, error) {
	return readFrom(ws.EventsFile(), 0)
}

// Since reads events after the first n lines (the cursor position).
func Since(ws *workspace.Workspace, n int) ([]Event, error) {
	return readFrom(ws.EventsFile(), n)
}

func readFrom(path string, skip int) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	i := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		i++
		if i <= skip {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

// Count returns the number of events currently in the log.
func Count(ws *workspace.Workspace) int {
	return countLines(ws.EventsFile())
}
