package handlers

import (
	"testing"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestMatchesEventRequiresTypeAndEveryExactMatch(t *testing.T) {
	config := workspace.HandlerConfig{
		On: []string{events.TaskMoved},
		Match: map[string]any{
			"task":         "TASK-0001",
			"data.to":      "done",
			"data.attempt": 2,
		},
		Lua: "hooks/test.lua",
	}
	event := events.Event{
		Type: events.TaskMoved, Task: "TASK-0001",
		Data: map[string]any{"to": "done", "attempt": float64(2)},
	}
	if !matchesEvent(config, event) {
		t.Fatal("expected type, top-level, nested, and numeric matches to pass")
	}

	for name, mutate := range map[string]func(*events.Event){
		"wrong type":   func(event *events.Event) { event.Type = events.TaskCreated },
		"wrong task":   func(event *events.Event) { event.Task = "TASK-0002" },
		"missing data": func(event *events.Event) { delete(event.Data, "to") },
	} {
		t.Run(name, func(t *testing.T) {
			copy := event
			copy.Data = map[string]any{"to": event.Data["to"], "attempt": event.Data["attempt"]}
			mutate(&copy)
			if matchesEvent(config, copy) {
				t.Fatal("unexpected match")
			}
		})
	}
}
