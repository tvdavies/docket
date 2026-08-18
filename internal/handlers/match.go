package handlers

import (
	"reflect"
	"strings"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/workspace"
)

// matchesEvent applies the event-type subscription and optional exact-value
// matches. Match keys are dotted paths such as "data.to". All entries must
// match; absent fields never match, including when the configured value is nil.
func matchesEvent(config workspace.HandlerConfig, event events.Event) bool {
	if !config.Matches(event.Type) {
		return false
	}
	for path, expected := range config.Match {
		actual, ok := eventPath(event, path)
		if !ok || !matchEqual(actual, expected) {
			return false
		}
	}
	return true
}

func eventPath(event events.Event, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var value any
	switch parts[0] {
	case "seq":
		value = event.Seq
	case "time":
		value = event.Time
	case "type":
		value = event.Type
	case "task":
		value = event.Task
	case "title":
		value = event.Title
	case "actor":
		value = event.Actor
	case "assignee":
		value = event.Assignee
	case "data":
		value = event.Data
	default:
		return nil, false
	}
	for _, part := range parts[1:] {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func matchEqual(actual, expected any) bool {
	if reflect.DeepEqual(actual, expected) {
		return true
	}
	a, aOK := numericValue(actual)
	b, bOK := numericValue(expected)
	return aOK && bOK && a == b
}

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}
