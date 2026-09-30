// Package widget reads legacy widget records from the event log. Earlier
// Docket releases let plugins publish task.widget_created and
// task.widget_finalised events for the retired web board. Nothing produces
// them now, but existing events remain history: this package validates and
// folds them so task bundles keep showing their summaries and references.
// New session or outcome information belongs in comments, references and
// attachments.
package widget

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tvdavies/docket/internal/events"
)

const Created = "task.widget_created"
const Finalised = "task.widget_finalised"
const MaxRecordBytes = 8 << 10
const MaxRevision = 9007199254740991

var typePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*/[a-zA-Z0-9][a-zA-Z0-9_/-]*$`)
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,199}$`)

type Reference struct {
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Title string `json:"title"`
}
type Fallback struct {
	Label       string      `json:"label"`
	StatusLabel string      `json:"status_label"`
	Summary     string      `json:"summary,omitempty"`
	Priority    string      `json:"priority"`
	StartedAt   string      `json:"started_at,omitempty"`
	EndedAt     string      `json:"ended_at,omitempty"`
	References  []Reference `json:"references,omitempty"`
}
type Record struct {
	Version    int      `json:"version"`
	WidgetType string   `json:"widget_type"`
	InstanceID string   `json:"instance_id"`
	TaskID     string   `json:"task_id"`
	CreatedAt  string   `json:"created_at"`
	Revision   int64    `json:"revision"`
	Phase      string   `json:"phase"`
	Fallback   Fallback `json:"fallback"`
}

// errInvalid rejects a record that does not match the published schema.
type errInvalid struct{}

func (errInvalid) Error() string { return "invalid_widget_record" }

func Key(r Record) string { return r.TaskID + "\x00" + r.WidgetType + "\x00" + r.InstanceID }
func text(v string, max int, required bool) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) <= max && (!required || strings.TrimSpace(v) != "") && !strings.ContainsRune(v, '\x00')
}
func timestamp(v string) bool { _, err := time.Parse(time.RFC3339Nano, v); return err == nil }
func SafeURL(v, kind, plugin, workspaceName string) bool {
	if !text(v, 2048, true) || strings.ContainsAny(v, "\\\r\n\t ") {
		return false
	}
	lower := strings.ToLower(v)
	for _, encoded := range []string{"%2e", "%2f", "%5c", "%25"} {
		if strings.Contains(lower, encoded) {
			return false
		}
	}
	for _, segment := range strings.Split(v, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	u, err := url.Parse(v)
	if err != nil || u.User != nil {
		return false
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return strings.HasPrefix(u.Path, "/plugins/"+plugin+"/") || (workspaceName != "" && strings.HasPrefix(u.Path, "/workspaces/"+url.PathEscape(workspaceName)+"/"))
	}
	return kind != "session" && kind != "task" && u.Scheme == "https" && u.Host != ""
}

// Validate checks a historical record against the schema it was published
// under, so hand-edited or malformed events never reach readers.
func Validate(r Record, workspaceName string) error {
	invalid := func() error { return errInvalid{} }
	if r.Version != 1 || !typePattern.MatchString(r.WidgetType) || len(r.WidgetType) > 100 || !idPattern.MatchString(r.InstanceID) || !idPattern.MatchString(r.TaskID) || !timestamp(r.CreatedAt) || r.Revision < 1 || r.Revision > MaxRevision || (r.Phase != "created" && r.Phase != "finalised") {
		return invalid()
	}
	f := r.Fallback
	if !text(f.Label, 120, true) || !text(f.StatusLabel, 120, true) || !text(f.Summary, 2000, false) || len(f.References) > 8 {
		return invalid()
	}
	switch f.Priority {
	case "attention", "error", "active", "history":
	default:
		return invalid()
	}
	if (f.StartedAt != "" && !timestamp(f.StartedAt)) || (f.EndedAt != "" && !timestamp(f.EndedAt)) {
		return invalid()
	}
	for _, ref := range f.References {
		if !text(ref.Title, 120, true) || !text(ref.Kind, 120, true) || !SafeURL(ref.URL, ref.Kind, strings.Split(r.WidgetType, "/")[0], workspaceName) {
			return invalid()
		}
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxRecordBytes {
		return invalid()
	}
	return nil
}

type History struct {
	Create  Record
	Current Record
}
type Index map[string]History

// Fold rejects malformed lifecycle events rather than projecting unvalidated private data.
func Fold(log []events.Event) Index {
	index := Index{}
	for _, event := range log {
		if event.Type != Created && event.Type != Finalised {
			continue
		}
		raw, err := json.Marshal(event.Data["record"])
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(raw, &r) != nil || r.TaskID != event.Task {
			continue
		}
		// Validate saved presentation too: hand-written or future event records
		// must not smuggle an unbounded/invalid fallback into older readers.
		workspaceName := ""
		for _, ref := range r.Fallback.References {
			if path, ok := strings.CutPrefix(ref.URL, "/workspaces/"); ok {
				segment, _, _ := strings.Cut(path, "/")
				workspaceName, _ = url.PathUnescape(segment)
				break
			}
		}
		if Validate(r, workspaceName) != nil {
			continue
		}
		key := Key(r)
		old, exists := index[key]
		if event.Type == Created && r.Phase == "created" && !exists {
			index[key] = History{Create: r, Current: r}
		}
		if event.Type == Finalised && r.Phase == "finalised" && exists && old.Current.Phase == "created" && r.CreatedAt == old.Create.CreatedAt && r.Revision > old.Current.Revision {
			old.Current = r
			index[key] = old
		}
	}
	return index
}
func (index Index) Records(taskID string) []Record {
	result := []Record{}
	for _, history := range index {
		if history.Current.TaskID == taskID {
			result = append(result, history.Current)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.CreatedAt != b.CreatedAt {
			ta, _ := time.Parse(time.RFC3339Nano, a.CreatedAt)
			tb, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
			if !ta.Equal(tb) {
				return ta.Before(tb)
			}
		}
		return Key(a) < Key(b)
	})
	return result
}
func Revision(records []Record) string {
	b, _ := json.Marshal(records)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
