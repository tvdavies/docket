package handlers_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

func cursorPath(ws *workspace.Workspace, name string) string {
	return filepath.Join(ws.HandlerStateDir(), name+".cursor")
}

func writeRawCursor(t *testing.T, ws *workspace.Workspace, name, body string) {
	t.Helper()
	if err := store.WriteAtomic(cursorPath(ws, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseCheckpointRejectsEveryDegradedForm(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	cases := map[string]string{
		"plain integer":    "3\n",
		"negative":         `{"position":-1,"prefix_hash":"` + valid + `"}`,
		"float":            `{"position":1.5,"prefix_hash":"` + valid + `"}`,
		"string position":  `{"position":"1","prefix_hash":"` + valid + `"}`,
		"missing hash":     `{"position":1}`,
		"short hash":       `{"position":1,"prefix_hash":"abc"}`,
		"uppercase hash":   `{"position":1,"prefix_hash":"` + strings.ToUpper(valid) + `"}`,
		"zero with hash":   `{"position":0,"prefix_hash":"` + valid + `"}`,
		"empty":            "",
		"array":            "[1]",
		"trailing garbage": `{"position":1,"prefix_hash":"` + valid + `"} extra`,
		"two objects":      `{"position":1,"prefix_hash":"` + valid + `"}{"position":2,"prefix_hash":"` + valid + `"}`,
		"null":             "null",
	}
	for name, body := range cases {
		if _, err := handlers.ParseCheckpoint([]byte(body)); err == nil {
			t.Errorf("%s: parsed %q without error", name, body)
		}
	}
	zero, err := handlers.ParseCheckpoint([]byte(`{"position":0,"prefix_hash":""}` + "\n"))
	if err != nil || zero.Position != 0 || zero.PrefixHash != "" {
		t.Fatalf("canonical zero checkpoint = %+v, %v", zero, err)
	}
	positive, err := handlers.ParseCheckpoint([]byte(`{"position":7,"prefix_hash":"` + valid + `"}`))
	if err != nil || positive.Position != 7 || positive.PrefixHash != valid {
		t.Fatalf("positive checkpoint = %+v, %v", positive, err)
	}
	if string(positive.Raw) != `{"position":7,"prefix_hash":"`+valid+`"}` {
		t.Fatalf("raw bytes were not preserved: %q", positive.Raw)
	}
}

func TestReadCheckpointValidatesAgainstLogWithoutDegrading(t *testing.T) {
	ws, _ := newWorkspace(t)
	appendEvent(t, ws, events.TaskCreated)
	appendEvent(t, ws, events.TaskCreated)
	hash2, _, err := events.PrefixHash(ws, 2)
	if err != nil {
		t.Fatal(err)
	}
	hash1, _, _ := events.PrefixHash(ws, 1)

	if _, err := handlers.ReadCheckpoint(ws, "absent"); !errors.Is(err, handlers.ErrCheckpointMissing) {
		t.Fatalf("missing cursor error = %v", err)
	}
	if handlers.Cursor(ws, "absent") != 0 {
		t.Fatal("replay-oriented Cursor should still degrade to zero")
	}

	writeRawCursor(t, ws, "exact", `{"position":2,"prefix_hash":"`+hash2+`"}`)
	checkpoint, err := handlers.ReadCheckpoint(ws, "exact")
	if err != nil || checkpoint.Position != 2 {
		t.Fatalf("exact checkpoint = %+v, %v", checkpoint, err)
	}

	writeRawCursor(t, ws, "older", `{"position":1,"prefix_hash":"`+hash1+`"}`)
	if checkpoint, err := handlers.ReadCheckpoint(ws, "older"); err != nil || checkpoint.Position != 1 {
		t.Fatalf("append-only growth must remain valid: %+v, %v", checkpoint, err)
	}

	writeRawCursor(t, ws, "ahead", `{"position":3,"prefix_hash":"`+hash2+`"}`)
	if _, err := handlers.ReadCheckpoint(ws, "ahead"); err == nil || !strings.Contains(err.Error(), "event log has 2 lines") {
		t.Fatalf("short log error = %v", err)
	}
	if handlers.Cursor(ws, "ahead") != 0 {
		t.Fatal("Cursor should degrade a short-log checkpoint to zero")
	}

	writeRawCursor(t, ws, "stale", `{"position":2,"prefix_hash":"`+hash1+`"}`)
	if _, err := handlers.ReadCheckpoint(ws, "stale"); err == nil || !strings.Contains(err.Error(), "prefix hash does not match") {
		t.Fatalf("stale hash error = %v", err)
	}

	writeRawCursor(t, ws, "plain", "2\n")
	if _, err := handlers.ReadCheckpoint(ws, "plain"); err == nil {
		t.Fatal("plain integer cursor accepted")
	}
	if handlers.Cursor(ws, "plain") != 0 {
		t.Fatal("legacy plain cursor should still replay through Cursor")
	}

	// Strict reads never write.
	for _, name := range []string{"ahead", "stale", "plain"} {
		data, err := os.ReadFile(cursorPath(ws, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"position":0`) {
			t.Fatalf("strict read mutated %s cursor to %q", name, data)
		}
	}
}

func TestTransferCheckpointLockedCopiesExactlyAndNeverTouchesSource(t *testing.T) {
	ws, _ := newWorkspace(t)
	appendEvent(t, ws, events.TaskCreated)
	hash1, _, _ := events.PrefixHash(ws, 1)
	source := `{"position":1,"prefix_hash":"` + hash1 + `"}` + "\n"
	writeRawCursor(t, ws, "legacy", source)
	before, _ := os.ReadFile(cursorPath(ws, "legacy"))

	if _, err := handlers.TransferCheckpointLocked(ws, "legacy", "plugin/legacy"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cursorPath(ws, "legacy"))
	if string(before) != string(after) {
		t.Fatal("source cursor bytes changed")
	}
	destination, err := handlers.ReadCheckpoint(ws, "plugin/legacy")
	if err != nil || destination.Position != 1 || destination.PrefixHash != hash1 {
		t.Fatalf("destination = %+v, %v", destination, err)
	}

	// Corrupt sources fail without creating a destination.
	writeRawCursor(t, ws, "corrupt", `{"position":1,"prefix_hash":"deadbeef"}`)
	if _, err := handlers.TransferCheckpointLocked(ws, "corrupt", "plugin/corrupt"); err == nil {
		t.Fatal("corrupt source transferred")
	}
	if _, err := os.Stat(cursorPath(ws, "plugin/corrupt")); !os.IsNotExist(err) {
		t.Fatalf("destination created from a corrupt source: %v", err)
	}
	if err := handlers.AdoptCursor(ws, "missing", "plugin/missing"); !errors.Is(err, handlers.ErrCheckpointMissing) {
		t.Fatalf("AdoptCursor missing source = %v", err)
	}
}

func TestWriteCheckpointLockedRejectsInvalidValues(t *testing.T) {
	ws, _ := newWorkspace(t)
	if err := handlers.WriteCheckpointLocked(ws, "x", handlers.Checkpoint{Position: -1}); err == nil {
		t.Fatal("negative position written")
	}
	if err := handlers.WriteCheckpointLocked(ws, "x", handlers.Checkpoint{Position: 0, PrefixHash: "abc"}); err == nil {
		t.Fatal("zero position with hash written")
	}
	if _, err := os.Stat(cursorPath(ws, "x")); !os.IsNotExist(err) {
		t.Fatal("invalid write created a cursor file")
	}
	if err := handlers.WriteCheckpointLocked(ws, "x", handlers.Checkpoint{Position: 0}); err != nil {
		t.Fatal(err)
	}
	if checkpoint, err := handlers.ReadCheckpoint(ws, "x"); err != nil || checkpoint.Position != 0 {
		t.Fatalf("explicit zero = %+v, %v", checkpoint, err)
	}
}

func TestWithHandlerLocksDeduplicatesNames(t *testing.T) {
	ws, _ := newWorkspace(t)
	// A duplicated name would deadlock on itself if not deduplicated; the
	// 30-second lock timeout would then fail the call.
	done := make(chan error, 1)
	go func() {
		done <- handlers.WithHandlerLocks(nil, ws, []string{"a", "b", "a"}, func() error { return nil })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate lock names deadlocked")
	}
}

func TestSeedCursorAtEndWaitsForHandlerLockAndPreservesPreparedCheckpoint(t *testing.T) {
	ws, _ := newWorkspace(t)
	appendEvent(t, ws, events.TaskCreated)
	appendEvent(t, ws, events.TaskCreated)
	hash1, _, _ := events.PrefixHash(ws, 1)

	locked := make(chan struct{})
	release := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- store.WithLock(handlers.LockPath(ws, "plugin/h"), func() error {
			close(locked)
			<-release
			// The handoff prepares the destination while it still holds the lock.
			return handlers.WriteCheckpointLocked(ws, "plugin/h", handlers.Checkpoint{Position: 1, PrefixHash: hash1})
		})
	}()
	<-locked
	seedDone := make(chan error, 1)
	go func() { seedDone <- handlers.SeedCursorAtEnd(ws, "plugin/h") }()
	select {
	case err := <-seedDone:
		t.Fatalf("seed completed while the handler lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	if err := <-seedDone; err != nil {
		t.Fatal(err)
	}
	checkpoint, err := handlers.ReadCheckpoint(ws, "plugin/h")
	if err != nil || checkpoint.Position != 1 {
		t.Fatalf("delayed seed overwrote the prepared checkpoint: %+v, %v", checkpoint, err)
	}

	// An explicit zero checkpoint is also preserved by seeding.
	if err := handlers.ResetCursor(ws, "plugin/zero"); err != nil {
		t.Fatal(err)
	}
	if err := handlers.SeedCursorAtEnd(ws, "plugin/zero"); err != nil {
		t.Fatal(err)
	}
	if checkpoint, err := handlers.ReadCheckpoint(ws, "plugin/zero"); err != nil || checkpoint.Position != 0 {
		t.Fatalf("seed replaced explicit zero: %+v, %v", checkpoint, err)
	}
	// A genuinely missing cursor seeds at the end.
	if err := handlers.SeedCursorAtEnd(ws, "plugin/new"); err != nil {
		t.Fatal(err)
	}
	if checkpoint, err := handlers.ReadCheckpoint(ws, "plugin/new"); err != nil || checkpoint.Position != 2 {
		t.Fatalf("fresh seed = %+v, %v", checkpoint, err)
	}
}

func TestWriteAtomicReportDistinguishesRenameFromDirectorySync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	report, err := store.WriteAtomicReport(path, []byte("x"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Renamed || !report.DirSynced || report.DirSyncErr != "" {
		t.Fatalf("report = %+v", report)
	}
	if _, err := store.WriteAtomicReport(filepath.Join(path, "child"), []byte("x"), 0o644); err == nil {
		t.Fatal("write under a regular file succeeded")
	}
}
