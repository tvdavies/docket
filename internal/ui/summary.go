package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tvdavies/docket/internal/bundle"
)

// Tone groups activity by meaning so the timeline can colour it.
type Tone int

// Activity tones.
const (
	ToneNeutral Tone = iota
	ToneGood
	ToneWarn
	ToneDanger
	ToneInfo
	ToneAccent
)

// Summary is one activity item described for a person.
type Summary struct {
	// Icon is a single-cell symbol for the kind of activity.
	Icon string
	// Tone selects the icon colour.
	Tone Tone
	// Text is a sentence fragment that follows the actor, e.g.
	// "moved backlog → in-progress".
	Text string
}

// Summarize describes an activity item as a human sentence. Known event
// types are phrased from their data; anything else falls back to the event
// type plus a compact rendering of its data, so nothing is hidden.
func Summarize(item bundle.ActivityView) Summary {
	data := item.Data
	switch item.Kind {
	case "comment":
		return Summary{Icon: "✎", Tone: ToneAccent, Text: "commented"}
	case "session":
		session := item.Session
		if session == "" {
			session = "a session"
		} else {
			session = "session " + session
		}
		if item.Type == "detach" {
			return Summary{Icon: "◇", Tone: ToneNeutral, Text: "detached " + session}
		}
		return Summary{Icon: "◆", Tone: ToneNeutral, Text: "attached " + session}
	case "widget":
		return Summary{Icon: "▣", Tone: ToneInfo, Text: "posted " + humanType(item.Type)}
	}

	switch item.Type {
	case "task.created":
		return Summary{Icon: "✦", Tone: ToneGood, Text: "created the task"}
	case "task.updated":
		return Summary{Icon: "✎", Tone: ToneNeutral, Text: "edited the task details"}
	case "task.moved":
		from, to := str(data, "from"), str(data, "to")
		if from == "" {
			return Summary{Icon: "→", Tone: ToneInfo, Text: "moved to " + orUnknown(to)}
		}
		return Summary{Icon: "→", Tone: ToneInfo, Text: fmt.Sprintf("moved %s → %s", from, orUnknown(to))}
	case "task.labeled":
		labels := strs(data, "labels")
		if len(labels) == 0 {
			return Summary{Icon: "#", Tone: ToneNeutral, Text: "cleared all labels"}
		}
		return Summary{Icon: "#", Tone: ToneNeutral, Text: "set labels to " + strings.Join(labels, ", ")}
	case "task.linked":
		return Summary{Icon: "⇄", Tone: ToneInfo, Text: strings.TrimSpace(fmt.Sprintf("linked %s %s", str(data, "kind"), str(data, "to")))}
	case "task.unlinked":
		return Summary{Icon: "⇄", Tone: ToneNeutral, Text: strings.TrimSpace(fmt.Sprintf("removed the %s link to %s", str(data, "kind"), str(data, "to")))}
	case "task.file_attached":
		text := "attached " + orUnknown(str(data, "file"))
		if mime := str(data, "mime"); mime != "" {
			text += " (" + mime + ")"
		}
		return Summary{Icon: "⎘", Tone: ToneInfo, Text: text}
	case "task.assigned":
		return Summary{Icon: "@", Tone: ToneInfo, Text: "changed the assignee"}
	case "task.waiting":
		wait := object(data, "wait")
		text := "started waiting"
		if kind := str(wait, "kind"); kind != "" {
			text += " on " + kind
		}
		if reason := str(wait, "reason"); reason != "" {
			text += ": " + reason
		}
		return Summary{Icon: "◷", Tone: ToneWarn, Text: text}
	case "task.resumed":
		text := "resumed"
		if kind := str(data, "kind"); kind != "" {
			text += " after the " + kind + " wait"
		}
		if result := str(data, "result"); result != "" {
			text += ": " + result
		}
		return Summary{Icon: "▶", Tone: ToneGood, Text: text}
	case "task.reference_added", "task.reference_removed":
		reference := object(data, "reference")
		verb, tone := "added", ToneInfo
		if item.Type == "task.reference_removed" {
			verb, tone = "removed", ToneNeutral
		}
		text := verb
		if kind := str(reference, "kind"); kind != "" {
			text += " " + kind
		}
		text += " reference"
		if label := firstNonEmpty(str(reference, "title"), str(reference, "url"), str(reference, "id")); label != "" {
			text += " " + label
		}
		return Summary{Icon: "↗", Tone: tone, Text: text}
	}

	text := humanType(item.Type)
	if details := compactData(data); details != "" {
		text += " · " + details
	}
	return Summary{Icon: "•", Tone: ToneNeutral, Text: text}
}

// humanType turns "task.something_done" into "something done".
func humanType(eventType string) string {
	text := strings.TrimPrefix(eventType, "task.")
	text = strings.NewReplacer("_", " ", ".", " ").Replace(text)
	if text == "" {
		return "activity"
	}
	return text
}

// compactData renders data as sorted key=value pairs, encoding nested values
// as compact JSON.
func compactData(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		var value string
		switch v := data[key].(type) {
		case string:
			value = v
		case nil:
			value = "null"
		default:
			encoded, err := json.Marshal(v)
			if err != nil {
				value = fmt.Sprint(v)
			} else {
				value = string(encoded)
			}
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ", ")
}

func str(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	switch v := data[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func strs(data map[string]any, key string) []string {
	switch v := data[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	}
	return nil
}

// object returns a nested object, normalising in-memory structs (as built by
// the CLI before the event round-trips through JSON) to a map.
func object(data map[string]any, key string) map[string]any {
	switch v := data[key].(type) {
	case map[string]any:
		return v
	case nil:
		return nil
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var out map[string]any
		if json.Unmarshal(encoded, &out) != nil {
			return nil
		}
		return out
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown status"
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ParseTime reads a stored RFC 3339 timestamp, returning the zero time when
// it cannot be parsed.
func ParseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// Relative describes t relative to now: "just now", "5m ago", "3d ago", or
// a date once it is more than a few weeks away.
func Relative(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	suffix := " ago"
	if d < 0 {
		d, suffix = -d, " from now"
	}
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm%s", int(d.Round(time.Minute)/time.Minute), suffix)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%s", int(d/time.Hour), suffix)
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd%s", int(d/(24*time.Hour)), suffix)
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dw%s", int(d/(7*24*time.Hour)), suffix)
	}
	if t.Year() == now.Year() {
		return t.Local().Format("2 Jan")
	}
	return t.Local().Format("2 Jan 2006")
}

// Absolute formats t in local time, e.g. "2026-10-13 14:02".
func Absolute(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}
