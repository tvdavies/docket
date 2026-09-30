package task

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

// Link creates a typed relationship from→to of the given kind and maintains
// the inverse edge on the target task. Both tasks are locked (in id order to
// avoid deadlock) and updated atomically.
func Link(ws *workspace.Workspace, from, kind, to string) error {
	return LinkWithCommit(ws, from, kind, to, nil)
}

// LinkWithCommit is Link plus a commit callback run while both task locks are
// held. A failed commit restores both original dossiers.
func LinkWithCommit(ws *workspace.Workspace, from, kind, to string, commit func(a, b *Task) error) error {
	rel, ok := ws.Config.RelByName(kind)
	if !ok {
		return fmt.Errorf("unknown relationship %q", kind)
	}
	if from == to {
		return fmt.Errorf("cannot link a task to itself")
	}
	return updateTwo(ws, from, to, func(a, b *Task) error {
		addRel(a, rel.Name, b.ID)
		if rel.Inverse != "" {
			addRel(b, rel.Inverse, a.ID)
		}
		return nil
	}, commit)
}

// Unlink removes a relationship and its inverse.
func Unlink(ws *workspace.Workspace, from, kind, to string) error {
	return UnlinkWithCommit(ws, from, kind, to, nil)
}

// UnlinkWithCommit is Unlink plus a commit callback run under both task locks.
func UnlinkWithCommit(ws *workspace.Workspace, from, kind, to string, commit func(a, b *Task) error) error {
	rel, ok := ws.Config.RelByName(kind)
	if !ok {
		return fmt.Errorf("unknown relationship %q", kind)
	}
	return updateTwo(ws, from, to, func(a, b *Task) error {
		removeRel(a, rel.Name, b.ID)
		if rel.Inverse != "" {
			removeRel(b, rel.Inverse, a.ID)
		}
		return nil
	}, commit)
}

func addRel(t *Task, kind, id string) {
	if t.Relationships == nil {
		t.Relationships = map[string][]string{}
	}
	for _, x := range t.Relationships[kind] {
		if x == id {
			return
		}
	}
	t.Relationships[kind] = append(t.Relationships[kind], id)
	sort.Strings(t.Relationships[kind])
}

func removeRel(t *Task, kind, id string) {
	if t.Relationships == nil {
		return
	}
	out := t.Relationships[kind][:0]
	for _, x := range t.Relationships[kind] {
		if x != id {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		delete(t.Relationships, kind)
	} else {
		t.Relationships[kind] = out
	}
}

// updateTwo locks two tasks in a stable order, mutates both, saves them, and
// runs commit before releasing either lock.
func updateTwo(ws *workspace.Workspace, idA, idB string, fn func(a, b *Task) error, commit func(a, b *Task) error) error {
	dirA, err := resolveDir(ws, idA)
	if err != nil {
		return err
	}
	dirB, err := resolveDir(ws, idB)
	if err != nil {
		return err
	}
	// Deterministic lock ordering by directory path prevents deadlock.
	first, second := dirA, dirB
	if first > second {
		first, second = second, first
	}
	return store.WithLock(filepath.Join(first, ".lock"), func() error {
		return store.WithLock(filepath.Join(second, ".lock"), func() error {
			originalA, err := os.ReadFile(filepath.Join(dirA, "task.md"))
			if err != nil {
				return err
			}
			originalB, err := os.ReadFile(filepath.Join(dirB, "task.md"))
			if err != nil {
				return err
			}
			a, err := loadDir(dirA)
			if err != nil {
				return err
			}
			b, err := loadDir(dirB)
			if err != nil {
				return err
			}
			if err := fn(a, b); err != nil {
				return err
			}
			a.UpdatedAt = Now()
			b.UpdatedAt = Now()
			restore := func(cause error) error {
				return errors.Join(cause,
					restoreDossier(a.TaskFile(), originalA),
					restoreDossier(b.TaskFile(), originalB))
			}
			if err := a.save(); err != nil {
				return restore(err)
			}
			if err := b.save(); err != nil {
				return restore(err)
			}
			if commit == nil {
				return nil
			}
			if err := commit(a, b); err != nil {
				if Committed(err) {
					return err
				}
				return restore(err)
			}
			return nil
		})
	})
}

func restoreDossier(path string, original []byte) error {
	if err := store.WriteAtomic(path, original, 0o644); err != nil {
		return fmt.Errorf("roll back task dossier: %w", err)
	}
	return nil
}
