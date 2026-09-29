package widget

import (
	"encoding/json"
	"testing"

	"github.com/tvdavies/docket/internal/events"
)

// historicalRecord is the published wire shape of a record written by an
// earlier Docket release.
const historicalRecord = `{"version":1,"widget_type":"fixture-progress/job","instance_id":"job-1","task_id":"TASK-1","created_at":"2026-09-10T10:00:00Z","revision":1,"phase":"created","fallback":{"label":"Fixture job","status_label":"Processing","priority":"active","references":[{"kind":"task","url":"/workspaces/fixture/tasks/TASK-1","title":"Original task"}]}}`

func recordEvent(t *testing.T, kind, raw string) events.Event {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	return events.Event{Type: kind, Task: "TASK-1", Data: map[string]any{"record": record}}
}

func TestHistoricalRecordsFoldToTheirLatestPhase(t *testing.T) {
	var record Record
	if err := json.Unmarshal([]byte(historicalRecord), &record); err != nil {
		t.Fatal(err)
	}
	if err := Validate(record, "fixture"); err != nil {
		t.Fatal(err)
	}
	finalised := `{"version":1,"widget_type":"fixture-progress/job","instance_id":"job-1","task_id":"TASK-1","created_at":"2026-09-10T10:00:00Z","revision":7,"phase":"finalised","fallback":{"label":"Fixture job","status_label":"Finished","summary":"Saved outcome","priority":"history"}}`
	stale := `{"version":1,"widget_type":"fixture-progress/job","instance_id":"job-1","task_id":"TASK-1","created_at":"2026-09-10T10:00:00Z","revision":3,"phase":"finalised","fallback":{"label":"Fixture job","status_label":"Late","priority":"history"}}`
	log := []events.Event{
		recordEvent(t, Finalised, finalised), // finalise before create is ignored
		recordEvent(t, Created, historicalRecord),
		recordEvent(t, Finalised, finalised),
		recordEvent(t, Finalised, stale), // after terminal: ignored
		{Type: Created, Task: "TASK-1", Data: map[string]any{"record": "not an object"}},
	}
	records := Fold(log).Records("TASK-1")
	if len(records) != 1 || records[0].Phase != "finalised" || records[0].Fallback.Summary != "Saved outcome" {
		t.Fatalf("records = %#v", records)
	}
	if Revision(records) == Revision(nil) {
		t.Fatal("revision ignores records")
	}
}

func TestHistoricalRecordsRejectUnsafeOrMalformedFields(t *testing.T) {
	var base Record
	if err := json.Unmarshal([]byte(historicalRecord), &base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Record){
		func(r *Record) { r.Version = 2 },
		func(r *Record) { r.Revision = MaxRevision + 1 },
		func(r *Record) { r.InstanceID = "../escape" },
		func(r *Record) { r.Fallback.Summary = string(make([]byte, 2001)) },
		func(r *Record) {
			r.Fallback.References = []Reference{{Kind: "session", URL: "https://evil.example/run", Title: "Unsafe"}}
		},
	} {
		r := base
		mutate(&r)
		if Validate(r, "fixture") == nil {
			t.Fatalf("invalid record accepted: %+v", r)
		}
	}
}

func TestSafeDestinations(t *testing.T) {
	for _, url := range []string{"//evil.test/run", "https://evil.test/run", "/plugins/other/run", "/plugins/fixture/../other", "/plugins/fixture/%2e%2e/other", "/workspaces/other/tasks/TASK-1", "javascript:alert(1)", "file:///etc/passwd"} {
		if SafeURL(url, "session", "fixture", "one") {
			t.Fatal("accepted", url)
		}
	}
	if !SafeURL("/plugins/fixture/sessions/one", "session", "fixture", "one") || !SafeURL("https://example.test/plan", "plan", "fixture", "one") {
		t.Fatal("safe URL rejected")
	}
}
