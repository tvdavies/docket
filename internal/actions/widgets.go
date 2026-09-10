package actions

import (
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/widget"
)

// Widget commits only lifecycle events; it never edits the task dossier. Preview
// publishers use this same task lock so a final record fences concurrent live writes.
func (operations Tasks) Widget(workspaceName, id string, record widget.Record) (current widget.Record, appended bool, err error) {
	if id != record.TaskID {
		return current, false, &widget.Error{Code: "invalid_widget_identity"}
	}
	if err = widget.Validate(record, workspaceName); err != nil {
		return
	}
	if !widget.Enabled(operations.Workspace, record.WidgetType) {
		return current, false, &widget.Error{Code: "widget_not_enabled"}
	}
	value, err := task.Load(operations.Workspace, id)
	if err != nil {
		return current, false, err
	}
	err = store.WithLock(value.LockFile(), func() error {
		index, err := widget.Load(operations.Workspace)
		if err != nil {
			return err
		}
		history, exists := index[widget.Key(record)]
		write, err := widget.Transition(history, exists, record)
		if err != nil {
			return err
		}
		if !write {
			current = history.Current
			return nil
		}
		kind := widget.Created
		if record.Phase == "finalised" {
			kind = widget.Finalised
		}
		if err := operations.append(events.Event{Type: kind, Task: id, Title: value.Title, Actor: operations.Actor, Data: map[string]any{"record": record}}); err != nil {
			return err
		}
		current = record
		appended = true
		return nil
	})
	return
}
