// Package events implements docket's append-only event log (events.jsonl) — the
// coordination primitive. Every mutation appends one JSON line. Consumers
// either poll a filtered view (the "inbox"), stream new lines (`docket watch`),
// or receive matching events through configured post-hoc handlers.
package events

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

// Event types.
const (
	TaskCreated    = "task.created"
	TaskUpdated    = "task.updated"
	TaskMoved      = "task.moved"
	TaskCommented  = "task.commented"
	TaskLabeled    = "task.labeled"
	TaskLinked     = "task.linked"
	TaskUnlinked   = "task.unlinked"
	TaskAttached   = "task.attached"
	TaskDetached   = "task.detached"
	FileAttached   = "task.file_attached"
	TaskAssigned   = "task.assigned"
	ProjectCreated = "project.created"
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
	evs, _, err := ReadBatch(ws, 0)
	return evs, err
}

// Since reads events after the first n non-empty lines (the cursor position).
func Since(ws *workspace.Workspace, n int) ([]Event, error) {
	evs, _, err := ReadBatch(ws, n)
	return evs, err
}

// ReadBatch reads valid events after cursor and returns the cursor position at
// the end of this snapshot. Malformed non-empty lines are skipped as events but
// included in end, so a consumer can advance past them instead of getting
// permanently stuck. Positions count non-empty lines, matching Count.
func ReadBatch(ws *workspace.Workspace, cursor int) ([]Event, int, error) {
	events, end, _, err := ReadBatchCheckpoint(ws, cursor)
	return events, end, err
}

// ReadBatchCheckpoint is ReadBatch plus a hash of the exact log prefix read.
// A durable consumer verifies this before advancing its cursor so a concurrent
// history replacement cannot acknowledge events it never received.
func ReadBatchCheckpoint(ws *workspace.Workspace, cursor int) ([]Event, int, string, error) {
	return readFrom(ws.EventsFile(), cursor)
}

func readFrom(path string, skip int) ([]Event, int, string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, skip, "", nil
		}
		return nil, skip, "", err
	}
	defer f.Close()
	var out []Event
	hash := sha256.New()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	i := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		i++
		_, _ = hash.Write(line)
		_, _ = hash.Write([]byte{'\n'})
		if i <= skip {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, i, fmt.Sprintf("%x", hash.Sum(nil)), sc.Err()
}

// PrefixHash returns a SHA-256 checkpoint for the first position non-empty log
// lines and the number actually found. Consumers persist this with a cursor so
// replacement, truncation, or history rewrites reset to safe replay instead of
// silently skipping events.
func PrefixHash(ws *workspace.Workspace, position int) (string, int, error) {
	if position <= 0 {
		return "", 0, nil
	}
	file, err := os.Open(ws.EventsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, nil
		}
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		count++
		_, _ = hash.Write(line)
		_, _ = hash.Write([]byte{'\n'})
		if count == position {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", count, err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), count, nil
}

// Count returns the number of events currently in the log.
func Count(ws *workspace.Workspace) int {
	return countLines(ws.EventsFile())
}
