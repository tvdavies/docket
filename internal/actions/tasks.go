// Package actions provides task mutations shared by the CLI and embedded hook
// SDKs. It keeps validation, locking, and event production identical regardless
// of which user-facing surface initiated an operation.
package actions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	return operations.appendAll([]events.Event{event})
}

func (operations Tasks) appendAll(group []events.Event) error {
	if operations.Append == nil {
		return events.AppendAll(operations.Workspace, group)
	}
	for _, event := range group {
		if err := operations.Append(event); err != nil {
			return err
		}
	}
	return nil
}

// Create creates a task and emits task.created.
func (operations Tasks) Create(options task.CreateOptions) (*task.Task, error) {
	value, err := task.Create(operations.Workspace, options)
	if err != nil {
		return nil, err
	}
	if err := operations.append(events.Event{
		Type: events.TaskCreated, Task: value.ID, Title: value.Title,
		Actor: operations.Actor, Assignee: value.Assignee,
	}); err != nil {
		rollbackErr := os.RemoveAll(value.Dir())
		if rollbackErr != nil {
			return nil, errors.Join(err, fmt.Errorf("roll back created task: %w", rollbackErr))
		}
		return nil, err
	}
	return value, nil
}

// EditOptions selects mutable dossier fields. Nil leaves a field unchanged;
// an empty Assignee explicitly clears assignment.
type EditOptions struct {
	Title       *string
	Description *string
	Assignee    *string
}

// Edit changes dossier fields and emits task.updated, or task.assigned when
// assignment is among the requested changes (matching the established CLI
// event contract).
func (operations Tasks) Edit(id string, options EditOptions) (*task.Task, error) {
	if options.Title == nil && options.Description == nil && options.Assignee == nil {
		return nil, fmt.Errorf("no task changes requested")
	}
	if options.Title != nil && strings.TrimSpace(*options.Title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}
	value, err := task.Update(operations.Workspace, id, func(value *task.Task) error {
		if options.Title != nil {
			value.Title = strings.TrimSpace(*options.Title)
		}
		if options.Description != nil {
			value.Description = strings.TrimRight(*options.Description, "\n")
		}
		if options.Assignee != nil {
			value.Assignee = *options.Assignee
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	eventType := events.TaskUpdated
	if options.Assignee != nil {
		eventType = events.TaskAssigned
	}
	if err := operations.append(events.Event{
		Type: eventType, Task: value.ID, Title: value.Title,
		Actor: operations.Actor, Assignee: value.Assignee,
	}); err != nil {
		return nil, err
	}
	return value, nil
}

// PatchOptions applies one atomic multi-field dossier update. Nil fields are
// unchanged; empty Assignee and Labels values explicitly clear them.
type PatchOptions struct {
	Title       *string
	Description *string
	Assignee    *string
	Labels      *[]string
	Status      *string
}

var errNoPatchChanges = errors.New("no effective task changes")

// Patch validates every field, writes all requested dossier changes once under
// one task lock, and appends the resulting ordered event group before releasing
// that lock. A failed event commit restores the original dossier.
func (operations Tasks) Patch(id string, options PatchOptions) (*task.Task, error) {
	if options.Title == nil && options.Description == nil && options.Assignee == nil && options.Labels == nil && options.Status == nil {
		return nil, fmt.Errorf("no task changes requested")
	}
	if options.Title != nil {
		trimmed := strings.TrimSpace(*options.Title)
		if trimmed == "" {
			return nil, fmt.Errorf("title cannot be empty")
		}
		options.Title = &trimmed
	}
	if options.Description != nil {
		trimmed := strings.TrimRight(*options.Description, "\n")
		options.Description = &trimmed
	}
	if options.Status != nil && !operations.Workspace.Config.HasStatus(*options.Status) {
		return nil, fmt.Errorf("unknown status %q (configured: %s)", *options.Status, strings.Join(operations.Workspace.Config.Statuses, ", "))
	}
	if options.Labels != nil {
		normalised := uniqueLabels(*options.Labels)
		options.Labels = &normalised
	}

	var titleOrDescriptionChanged, assigneeChanged, labelsChanged, statusChanged bool
	var fromStatus string
	result, err := task.UpdateWithCommit(operations.Workspace, id, func(value *task.Task) error {
		fromStatus = value.Status
		if options.Title != nil && *options.Title != value.Title {
			value.Title = *options.Title
			titleOrDescriptionChanged = true
		}
		if options.Description != nil && *options.Description != value.Description {
			value.Description = *options.Description
			titleOrDescriptionChanged = true
		}
		if options.Assignee != nil && *options.Assignee != value.Assignee {
			value.Assignee = *options.Assignee
			assigneeChanged = true
		}
		if options.Labels != nil && !slices.Equal(*options.Labels, value.Labels) {
			value.Labels = append([]string(nil), (*options.Labels)...)
			labelsChanged = true
		}
		if options.Status != nil && *options.Status != value.Status {
			value.Status = *options.Status
			statusChanged = true
		}
		if !titleOrDescriptionChanged && !assigneeChanged && !labelsChanged && !statusChanged {
			return errNoPatchChanges
		}
		return nil
	}, func(value *task.Task) error {
		group := make([]events.Event, 0, 3)
		if assigneeChanged {
			group = append(group, events.Event{
				Type: events.TaskAssigned, Task: value.ID, Title: value.Title,
				Actor: operations.Actor, Assignee: value.Assignee,
			})
		} else if titleOrDescriptionChanged {
			group = append(group, events.Event{
				Type: events.TaskUpdated, Task: value.ID, Title: value.Title,
				Actor: operations.Actor, Assignee: value.Assignee,
			})
		}
		if labelsChanged {
			group = append(group, events.Event{
				Type: events.TaskLabeled, Task: value.ID, Title: value.Title,
				Actor: operations.Actor, Assignee: value.Assignee,
				Data: map[string]any{"labels": value.Labels},
			})
		}
		if statusChanged {
			group = append(group, events.Event{
				Type: events.TaskMoved, Task: value.ID, Title: value.Title,
				Actor: operations.Actor, Assignee: value.Assignee,
				Data: map[string]any{"from": fromStatus, "to": value.Status},
			})
		}
		return operations.appendAll(group)
	})
	if errors.Is(err, errNoPatchChanges) {
		return task.Load(operations.Workspace, id)
	}
	return result, err
}

func uniqueLabels(labels []string) []string {
	result := make([]string, 0, len(labels))
	seen := map[string]bool{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label != "" && !seen[label] {
			seen[label] = true
			result = append(result, label)
		}
	}
	return result
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
		rollbackErr := os.Remove(filepath.Join(value.CommentsDir(), comment.File))
		if rollbackErr != nil && !os.IsNotExist(rollbackErr) {
			return nil, errors.Join(err, fmt.Errorf("roll back comment: %w", rollbackErr))
		}
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
