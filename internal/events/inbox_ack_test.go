package events_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
)

func TestPeekInboxDoesNotMoveCursorAndAckIsIdempotent(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "sal"})
	_ = events.Append(ws, events.Event{Type: events.TaskCommented, Task: "T2", Assignee: "other"})

	first, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.From != 0 || first.To != 2 || first.Checkpoint == "" {
		t.Fatalf("peek = %#v", first)
	}
	// A consumer that crashed before durable intake rereads the same batch.
	again, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Events) != 1 || again.Checkpoint != first.Checkpoint {
		t.Fatalf("replay differs: %#v", again)
	}
	if events.Cursor(ws, "sal") != 0 {
		t.Fatal("peek moved the cursor")
	}

	ack, err := events.AckInbox(ws, "sal", false, first.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !ack.Applied || ack.Position != 2 {
		t.Fatalf("ack = %#v", ack)
	}
	// A crash after acknowledgement but before the consumer noted it retries.
	dup, err := events.AckInbox(ws, "sal", false, first.Checkpoint)
	if err != nil {
		t.Fatalf("duplicate ack: %v", err)
	}
	if dup.Applied || dup.Position != 2 {
		t.Fatalf("duplicate ack = %#v", dup)
	}
	next, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Events) != 0 || next.From != 2 {
		t.Fatalf("after ack = %#v", next)
	}
}

func TestPeekInboxEmptyFilteredBatchStillAdvances(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskCreated, Task: "T1", Assignee: "other"})
	batch, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Events == nil || len(batch.Events) != 0 || batch.To != 1 {
		t.Fatalf("batch = %#v", batch)
	}
	if _, err := events.AckInbox(ws, "sal", false, batch.Checkpoint); err != nil {
		t.Fatal(err)
	}
	if events.Cursor(ws, "sal") != 1 {
		t.Fatal("empty filtered batch did not acknowledge inspected records")
	}
}

func TestAckInboxRejectsStaleOutOfOrderAndForeignCheckpoints(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "sal"})
	first, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "sal"})
	second, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}

	for name, call := range map[string]func() error{
		"wrong actor": func() error { _, err := events.AckInbox(ws, "bob", false, first.Checkpoint); return err },
		"wrong mode":  func() error { _, err := events.AckInbox(ws, "sal", true, first.Checkpoint); return err },
		"garbage":     func() error { _, err := events.AckInbox(ws, "sal", false, "nonsense"); return err },
		"tampered": func() error {
			_, err := events.AckInbox(ws, "sal", false, first.Checkpoint[:len(first.Checkpoint)-4]+"AAAA")
			return err
		},
		"wrong workspace": func() error {
			other := newWorkspace(t)
			_ = events.Append(other, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "sal"})
			_, err := events.AckInbox(other, "sal", false, first.Checkpoint)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: ack succeeded", name)
		}
		if events.Cursor(ws, "sal") != 0 {
			t.Fatalf("%s: rejected ack moved cursor", name)
		}
	}

	// Acknowledging the larger batch first makes the earlier one a no-op
	// rather than a regression.
	if ack, err := events.AckInbox(ws, "sal", false, second.Checkpoint); err != nil || ack.Position != 2 {
		t.Fatalf("second ack = %#v, %v", ack, err)
	}
	if ack, err := events.AckInbox(ws, "sal", false, first.Checkpoint); err != nil || ack.Applied || ack.Position != 2 {
		t.Fatalf("out-of-order earlier ack = %#v, %v", ack, err)
	}

	// A checkpoint read from a position the cursor has since left behind by
	// another path (legacy --mark-read) is stale when it would skip ahead.
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "sal"})
	third, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.ResetInbox(ws, "sal"); err != nil {
		t.Fatal(err)
	}
	if _, err := events.AckInbox(ws, "sal", false, third.Checkpoint); !errors.Is(err, events.ErrInboxCheckpointStale) {
		t.Fatalf("stale ack err = %v", err)
	}
	if events.Cursor(ws, "sal") != 0 {
		t.Fatal("stale ack moved cursor")
	}
}

func TestInboxAckSurvivesConcurrentAppend(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "sal"})
	batch, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "sal"})
	if _, err := events.AckInbox(ws, "sal", false, batch.Checkpoint); err != nil {
		t.Fatal(err)
	}
	next, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Events) != 1 || next.Events[0].Type != events.TaskMoved {
		t.Fatalf("event appended after the peek was acknowledged: %#v", next)
	}
}

func TestInboxSkipsMalformedRecordsButCountsThem(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "sal"})
	file, err := os.OpenFile(ws.EventsFile(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("{not json}\n")
	_ = file.Close()
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "sal"})
	batch, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Events) != 2 || batch.To != 3 {
		t.Fatalf("batch = %#v", batch)
	}
	if _, err := events.AckInbox(ws, "sal", false, batch.Checkpoint); err != nil {
		t.Fatal(err)
	}
	if events.Cursor(ws, "sal") != 3 {
		t.Fatal("malformed record left the consumer stuck")
	}
}

func TestInboxDetectsTruncatedAndReplacedHistory(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "sal"})
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "sal"})
	batch, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(ws.EventsFile())
	if err != nil {
		t.Fatal(err)
	}

	// Replaced before acknowledgement: the checkpoint no longer describes
	// the log, so it must not acknowledge unrelated replacement events.
	replaced := strings.Replace(string(original), "T1", "T9", 1)
	if err := os.WriteFile(ws.EventsFile(), []byte(replaced), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := events.AckInbox(ws, "sal", false, batch.Checkpoint); !errors.Is(err, events.ErrInboxHistoryChanged) {
		t.Fatalf("ack over replaced history err = %v", err)
	}
	if events.Cursor(ws, "sal") != 0 {
		t.Fatal("rejected ack moved cursor")
	}

	// Acknowledged, then truncated: the next read reports the change rather
	// than silently resuming against shorter history.
	if err := os.WriteFile(ws.EventsFile(), original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := events.AckInbox(ws, "sal", false, batch.Checkpoint); err != nil {
		t.Fatal(err)
	}
	firstLine := original[:strings.IndexByte(string(original), '\n')+1]
	if err := os.WriteFile(ws.EventsFile(), firstLine, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := events.PeekInbox(ws, "sal", false); !errors.Is(err, events.ErrInboxHistoryChanged) {
		t.Fatalf("truncated peek err = %v", err)
	}

	// Rewritten beneath the cursor at the same length is also detected.
	if err := os.WriteFile(ws.EventsFile(), []byte(replaced), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := events.PeekInbox(ws, "sal", false); !errors.Is(err, events.ErrInboxHistoryChanged) {
		t.Fatalf("rewritten peek err = %v", err)
	}

	// Explicit recovery replays the current log.
	if err := events.ResetInbox(ws, "sal"); err != nil {
		t.Fatal(err)
	}
	recovered, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Events) != 2 || recovered.Events[0].Task != "T9" {
		t.Fatalf("recovered = %#v", recovered)
	}
}

func TestLegacyNumericInboxCursorMigratesLazily(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "sal"})
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "sal"})
	// Fixture: a cursor written by an earlier release is a bare line count
	// with no checkpoint file.
	if err := os.MkdirAll(ws.CursorsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cursorPath := filepath.Join(ws.CursorsDir(), "sal.cursor")
	if err := os.WriteFile(cursorPath, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	batch, err := events.PeekInbox(ws, "sal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Events) != 1 || batch.Events[0].Type != events.TaskMoved || batch.From != 1 {
		t.Fatalf("legacy cursor batch = %#v", batch)
	}
	if _, err := events.AckInbox(ws, "sal", false, batch.Checkpoint); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "2" {
		t.Fatalf("cursor file format changed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(ws.CursorsDir(), "sal.checkpoint")); err != nil {
		t.Fatalf("checkpoint not recorded on migration: %v", err)
	}
	// An older binary advancing only the numeric cursor leaves the
	// checkpoint mismatched; it is then ignored rather than trusted.
	_ = events.Append(ws, events.Event{Type: events.TaskMoved, Task: "T1", Assignee: "sal"})
	if err := os.WriteFile(cursorPath, []byte("3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := events.PeekInbox(ws, "sal", false)
	if err != nil || after.From != 3 || len(after.Events) != 0 {
		t.Fatalf("after legacy advance = %#v, %v", after, err)
	}
}

func TestInboxAndHandlerCursorsStayIsolated(t *testing.T) {
	ws := newWorkspace(t)
	_ = events.Append(ws, events.Event{Type: events.TaskAssigned, Task: "T1", Assignee: "notify"})
	batch, err := events.PeekInbox(ws, "notify", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.AckInbox(ws, "notify", true, batch.Checkpoint); err != nil {
		t.Fatal(err)
	}
	if handlers.Cursor(ws, "notify") != 0 {
		t.Fatal("inbox acknowledgement moved a handler cursor of the same name")
	}
	if err := handlers.SeedCursorAtEnd(ws, "sal"); err != nil {
		t.Fatal(err)
	}
	if events.Cursor(ws, "sal") != 0 {
		t.Fatal("handler cursor moved an inbox cursor of the same name")
	}
}
