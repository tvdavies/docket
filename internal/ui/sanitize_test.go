package ui

import (
	"strings"
	"testing"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
)

func TestSanitizeStripsTerminalControlSequences(t *testing.T) {
	cases := []struct{ name, in, want, line string }{
		{"plain", "Fix login cache", "Fix login cache", "Fix login cache"},
		{"unicode", "Café ✓ 日本", "Café ✓ 日本", "Café ✓ 日本"},
		{"newline and tab", "a\nb\tc", "a\nb\tc", "a b c"},
		{"carriage return and bell", "safe\rEVIL\x07", "safeEVIL", "safeEVIL"},
		{"backspace and DEL", "ab\x08\x7fc", "abc", "abc"},
		{"SGR", "\x1b[31mred\x1b[0m", "red", "red"},
		{"CSI clear screen", "x\x1b[2J\x1b[Hy", "xy", "xy"},
		{"private CSI", "x\x1b[?1049hy", "xy", "xy"},
		{"OSC title BEL", "\x1b]0;pwned\x07title", "title", "title"},
		{"OSC hyperlink ST", "\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\", "link", "link"},
		{"OSC 52 clipboard", "\x1b]52;c;ZXZpbA==\x07ok", "ok", "ok"},
		{"DCS", "\x1bPq#0;2;0;0;0\x1b\\after", "after", "after"},
		{"APC", "\x1b_Gf=100;AAAA\x1b\\after", "after", "after"},
		{"unterminated OSC", "before\x1b]0;never ends", "before", "before"},
		{"two-byte escape", "a\x1bcb\x1b(Bc", "abc", "abc"},
		{"trailing ESC", "a\x1b", "a", "a"},
		{"C1 CSI", "a\u009b2Jb", "ab", "ab"},
		{"C1 OSC with ST", "a\u009d0;t\u009cb", "ab", "ab"},
		{"other C1", "a\u0085b", "ab", "ab"},
		{"raw 8-bit CSI byte", "a\x9b2Jb", "a\uFFFD2Jb", "a\uFFFD2Jb"},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("%s: Sanitize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
		if got := SanitizeLine(c.in); got != c.line {
			t.Errorf("%s: SanitizeLine(%q) = %q, want %q", c.name, c.in, got, c.line)
		}
	}
}

const evil = "\x1b]0;pwned\x07\x1b[2J\r\x9b"

// assertNoInjection fails if out contains anything only an injected payload
// could have produced.
func assertNoInjection(t *testing.T, out string) {
	t.Helper()
	for _, bad := range []string{"\x1b]", "\x07", "[2J", "\r", "pwned", "\u009b"} {
		if strings.Contains(out, bad) {
			t.Fatalf("styled output contains %q:\n%q", bad, out)
		}
	}
}

func evilBundle() *bundle.Bundle {
	return &bundle.Bundle{
		ID: "TASK-0001", Title: "Title" + evil, Status: "todo" + evil,
		CreatedAt: "2026-10-10T09:00:00Z", UpdatedAt: "2026-10-13T09:00:00Z",
		Project:       &bundle.ProjectRef{ID: "PROJ-0001", Name: "Proj" + evil},
		Labels:        []string{"bug" + evil},
		Assignee:      "alice" + evil,
		Wait:          &task.Wait{ID: "w1", Kind: "review" + evil, Reason: "reason" + evil, Reference: "https://x" + evil, Actor: "bob" + evil},
		References:    []task.Reference{{ID: "ref-1", Kind: "pr" + evil, URL: "https://y" + evil, Title: "ref" + evil}},
		Description:   "Body " + evil + "\n\nmore",
		Relationships: map[string][]bundle.TaskRef{"blocks": {{ID: "TASK-0002", Title: "rel" + evil}}},
		Attachments:   []*task.Attachment{{File: "f.log", Mime: "text/plain", Caption: "cap" + evil, AddedBy: "eve" + evil}},
		Activity: []bundle.ActivityView{
			{At: "2026-10-12T09:00:00Z", Kind: "event", Type: "task.updated", Actor: "mallory" + evil, Data: map[string]any{"title": "new" + evil, "labels": []any{"x" + evil}}},
			{At: "2026-10-12T09:05:00Z", Kind: "event", Type: "task.moved", Actor: "a", Data: map[string]any{"from": "b" + evil, "to": "c" + evil}},
			{At: "2026-10-13T10:00:00Z", Kind: "comment", Type: "comment", Actor: "bob", Body: "comment " + evil},
			{At: "2026-10-13T11:00:00Z", Kind: "event", Type: "task.plugin_ping", Data: map[string]any{"nested": map[string]any{"k": "v" + evil}}},
		},
	}
}

func TestStyledTaskViewNeutralisesInjectedControlSequences(t *testing.T) {
	b := evilBundle()
	value := FromBundle(b)
	for _, theme := range []Theme{NewTheme(true), plainRenderer(80).Theme} {
		r := Renderer{Theme: theme, Width: 80, Now: testNow}
		assertNoInjection(t, r.Document(value))
		assertNoInjection(t, r.Overview(value))
		assertNoInjection(t, r.Comments(value))
	}
	assertNoInjection(t, plainRenderer(80).Document(FromContext(b.State())))

	// The source data is untouched: plain and --json output still print it
	// byte for byte.
	if b.Title != "Title"+evil || b.Activity[2].Body != "comment "+evil || b.Attachments[0].Caption != "cap"+evil {
		t.Fatalf("FromBundle modified the bundle: %+v", b)
	}
}

func TestTaskTableNeutralisesInjectedControlSequences(t *testing.T) {
	tasks := []*task.Task{{ID: "TASK-0001", Title: "Title" + evil, Status: "todo" + evil, Labels: []string{"l" + evil}, Wait: &task.Wait{Kind: "k" + evil}}}
	assertNoInjection(t, NewTheme(true).TaskTable(tasks, 120))
	if tasks[0].Title != "Title"+evil {
		t.Fatal("TaskTable modified the task")
	}
}
