package task

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

// Link creates a typed relationship from→to of the given kind and maintains
// the inverse edge on the target task. Both tasks are locked (in id order to
// avoid deadlock) and updated atomically.
func Link(ws *workspace.Workspace, from, kind, to string) error {
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
	})
}

// Unlink removes a relationship and its inverse.
func Unlink(ws *workspace.Workspace, from, kind, to string) error {
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
	})
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

// updateTwo locks two tasks in a stable order, mutates both, and saves them.
func updateTwo(ws *workspace.Workspace, idA, idB string, fn func(a, b *Task) error) error {
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
			if err := a.save(); err != nil {
				return err
			}
			return b.save()
		})
	})
}
