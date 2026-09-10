package handlers_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/store"
)

// This reproduces SeedCursorAtEnd's unlocked Stat -> Count/PrefixHash -> write
// sequence at 30ec1b1/a019053, with an explicit scheduler barrier after Stat.
// It is a counterexample to mixed-version safety, not a test of the deployed
// service executable. Such runtimes are unsupported for a concurrent handoff.
func TestLegacyUnlockedSeedCanOverwritePreparedCheckpoint(t *testing.T) {
	ws, _ := newWorkspace(t)
	appendEvent(t, ws, events.TaskCreated)
	appendEvent(t, ws, events.TaskCreated)
	path := cursorPath(ws, "plugin/h")
	checked, continueSeed := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			done <- err
			return
		}
		close(checked)
		<-continueSeed
		position := events.Count(ws)
		hash, _, err := events.PrefixHash(ws, position)
		if err != nil {
			done <- err
			return
		}
		data, err := json.Marshal(map[string]any{"position": position, "prefix_hash": hash})
		if err != nil {
			done <- err
			return
		}
		done <- store.WriteAtomic(path, data, 0o644)
	}()
	<-checked
	err := handlers.WithHandlerLocks(nil, ws, []string{"plugin/h"}, func() error {
		hash, _, err := events.PrefixHash(ws, 1)
		if err != nil {
			return err
		}
		if err := handlers.WriteCheckpointLocked(ws, "plugin/h", handlers.Checkpoint{Position: 1, PrefixHash: hash}); err != nil {
			return err
		}
		close(continueSeed)
		// The old seeder ignores this held lock and overwrites a prepared prefix.
		return <-done
	})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := handlers.ReadCheckpoint(ws, "plugin/h")
	if err != nil || cp.Position != 2 {
		t.Fatalf("legacy seeding counterexample: %+v, %v", cp, err)
	}
	t.Log("UNSUPPORTED old unlocked seeder skipped pending event 2 despite the handoff lock")
}
