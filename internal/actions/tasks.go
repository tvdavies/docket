// Package actions provides task mutations shared by the CLI and embedded hook
// SDKs. It keeps validation, locking, and event production identical regardless
// of which user-facing surface initiated an operation.
package actions

import (
	"fmt"
	"strings"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

// AppendEvent durably records an event after its mutation succeeds.
type AppendEvent func(events.Event) error

// Tasks performs task operations for one actor and session.
type Tasks struct {
	Workspace *workspace.Workspace
	Actor     string
	Session   string
	Append    AppendEvent
}

func (operations Tasks) append(event events.Event) error {
	if operations.Append == nil {
		return events.Append(operations.Workspace, event)
	}
	return operations.Append(event)
}

// Move changes a task's status and emits task.moved.
func (operations Tasks) Move(id, status string) (*task.Task, string, error) {
	if !operations.Workspace.Config.HasStatus(status) {
		return nil, "", fmt.Errorf("unknown status %q (configured: %s)", status, strings.Join(operations.Workspace.Config.Statuses, ", "))
	}
	var from string
	value, err := task.Update(operations.Workspace, id, func(value *task.Task) error {
		from = value.Status
		value.Status = status
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if err := operations.append(events.Event{
		Type: events.TaskMoved, Task: value.ID, Title: value.Title,
		Actor: operations.Actor, Assignee: value.Assignee,
		Data: map[string]any{"from": from, "to": status},
	}); err != nil {
		return nil, "", err
	}
	return value, from, nil
}

// Assign changes a task's assignee and emits task.assigned.
func (operations Tasks) Assign(id, assignee string) (*task.Task, error) {
	value, err := task.Update(operations.Workspace, id, func(value *task.Task) error {
		value.Assignee = assignee
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := operations.append(events.Event{
		Type: events.TaskAssigned, Task: value.ID, Title: value.Title,
		Actor: operations.Actor, Assignee: value.Assignee,
	}); err != nil {
		return nil, err
	}
	return value, nil
}

// Comment appends a comment and emits task.commented.
func (operations Tasks) Comment(id, text string) (*task.Comment, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("comment text is required")
	}
	value, err := task.Load(operations.Workspace, id)
	if err != nil {
		return nil, err
	}
	comment, err := task.AddComment(operations.Workspace, id, operations.Actor, operations.Session, text)
	if err != nil {
		return nil, err
	}
	if err := operations.append(events.Event{
		Type: events.TaskCommented, Task: value.ID, Title: value.Title,
		Actor: operations.Actor, Assignee: value.Assignee,
	}); err != nil {
		return nil, err
	}
	return comment, nil
}

// Label adds and removes labels while preserving order and emits task.labeled.
func (operations Tasks) Label(id string, add, remove []string) (*task.Task, error) {
	value, err := task.Update(operations.Workspace, id, func(value *task.Task) error {
		value.Labels = applyLabels(value.Labels, add, remove)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := operations.append(events.Event{
		Type: events.TaskLabeled, Task: value.ID, Title: value.Title,
		Actor: operations.Actor, Assignee: value.Assignee,
		Data: map[string]any{"labels": value.Labels},
	}); err != nil {
		return nil, err
	}
	return value, nil
}

func applyLabels(existing, add, remove []string) []string {
	set := map[string]bool{}
	order := make([]string, 0, len(existing)+len(add))
	for _, label := range existing {
		if !set[label] {
			set[label] = true
			order = append(order, label)
		}
	}
	for _, label := range add {
		if !set[label] {
			set[label] = true
			order = append(order, label)
		}
	}
	removed := map[string]bool{}
	for _, label := range remove {
		removed[label] = true
	}
	result := make([]string, 0, len(order))
	for _, label := range order {
		if !removed[label] {
			result = append(result, label)
		}
	}
	return result
}
