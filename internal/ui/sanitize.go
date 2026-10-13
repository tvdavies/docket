package ui

import (
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
)

// Task data is written by people and agents, and styled output sends it
// straight to a terminal. Sanitize removes anything a terminal would act on
// rather than print: C0 and C1 control characters and whole ESC, CSI, OSC,
// DCS, SOS, PM, and APC sequences (a title must not be able to set the
// window title, rewrite the clipboard, or move the cursor). Invalid UTF-8 is
// replaced so a stray 8-bit C1 byte cannot reach the terminal either.
//
// Sanitize keeps newlines and tabs, for multi-line bodies. SanitizeLine also
// turns them into spaces, for single-line fields such as titles and labels.
//
// Only styled output (terminal views and the board) is sanitised; plain and
// --json output keep the data byte for byte.
func Sanitize(s string) string { return sanitize(s, false) }

// SanitizeLine is Sanitize for single-line fields: newlines and tabs become
// spaces.
func SanitizeLine(s string) string { return sanitize(s, true) }

func sanitize(s string, line bool) string {
	if isClean(s, line) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			b.WriteRune(utf8.RuneError)
			i += max(size, 1)
		case r == 0x1b:
			i = skipEscape(s, i+size)
		case r == 0x9b: // C1 CSI
			i = skipCSI(s, i+size)
		case r == 0x90 || r == 0x9d || r == 0x98 || r == 0x9e || r == 0x9f: // C1 DCS, OSC, SOS, PM, APC
			i = skipString(s, i+size)
		case r == '\n' || r == '\t':
			if line {
				b.WriteByte(' ')
			} else {
				b.WriteRune(r)
			}
			i += size
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			i += size // other C0 controls, DEL, and C1 controls
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// isClean reports whether s needs no changes, so the common case allocates
// nothing.
func isClean(s string, line bool) bool {
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			return false
		case r == '\n' || r == '\t':
			if line {
				return false
			}
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			return false
		}
	}
	return true
}

// skipEscape skips the sequence introduced by an ESC whose following byte is
// at i, returning the index just past it.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']', 'P', 'X', '^', '_':
		return skipString(s, i+1)
	}
	// nF sequences: intermediates 0x20–0x2F then one final byte.
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipCSI skips parameter and intermediate bytes up to and including the
// final byte. A malformed sequence ends at the first byte that cannot be
// part of it, which is then processed normally.
func skipCSI(s string, i int) int {
	for i < len(s) {
		c := s[i]
		switch {
		case c >= 0x20 && c <= 0x3f:
			i++
		case c >= 0x40 && c <= 0x7e:
			return i + 1
		default:
			return i
		}
	}
	return i
}

// skipString skips a control string (OSC, DCS, SOS, PM, APC) up to and
// including its terminator: BEL, ESC \, or C1 ST. An unterminated string runs
// to the end of s.
func skipString(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			return i + 1
		case strings.HasPrefix(s[i:], "\u009c"):
			return i + len("\u009c")
		}
		i++
	}
	return i
}

func sanitizeLines(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = SanitizeLine(v)
	}
	return out
}

// SanitizeTask returns a copy of t with every displayed field sanitised. t is
// not modified.
func SanitizeTask(t *task.Task) *task.Task {
	if t == nil {
		return nil
	}
	c := *t
	c.ID = SanitizeLine(t.ID)
	c.Title = SanitizeLine(t.Title)
	c.Status = SanitizeLine(t.Status)
	c.Project = SanitizeLine(t.Project)
	c.Labels = sanitizeLines(t.Labels)
	c.Assignee = SanitizeLine(t.Assignee)
	c.Description = Sanitize(t.Description)
	c.Wait = sanitizeWait(t.Wait)
	c.References = sanitizeReferences(t.References)
	if t.Relationships != nil {
		c.Relationships = make(map[string][]string, len(t.Relationships))
		for kind, ids := range t.Relationships {
			c.Relationships[SanitizeLine(kind)] = sanitizeLines(ids)
		}
	}
	return &c
}

func sanitizeWait(w *task.Wait) *task.Wait {
	if w == nil {
		return nil
	}
	c := *w
	c.ID = SanitizeLine(w.ID)
	c.Kind = SanitizeLine(w.Kind)
	c.Reason = Sanitize(w.Reason)
	c.Reference = SanitizeLine(w.Reference)
	c.Actor = SanitizeLine(w.Actor)
	return &c
}

func sanitizeReferences(refs []task.Reference) []task.Reference {
	if refs == nil {
		return nil
	}
	out := make([]task.Reference, len(refs))
	for i, r := range refs {
		r.ID = SanitizeLine(r.ID)
		r.Kind = SanitizeLine(r.Kind)
		r.URL = SanitizeLine(r.URL)
		r.Title = SanitizeLine(r.Title)
		r.AddedBy = SanitizeLine(r.AddedBy)
		out[i] = r
	}
	return out
}

// sanitized returns a copy of t with every user-controlled string sanitised.
// FromBundle and FromContext apply it, so everything the renderer draws has
// passed through it.
func (t Task) sanitized() Task {
	t.View = SanitizeLine(t.View)
	t.ID = SanitizeLine(t.ID)
	t.Title = SanitizeLine(t.Title)
	t.Status = SanitizeLine(t.Status)
	t.CreatedAt = SanitizeLine(t.CreatedAt)
	t.UpdatedAt = SanitizeLine(t.UpdatedAt)
	if t.Project != nil {
		p := bundle.ProjectRef{ID: SanitizeLine(t.Project.ID), Name: SanitizeLine(t.Project.Name)}
		t.Project = &p
	}
	t.Labels = sanitizeLines(t.Labels)
	t.Assignee = SanitizeLine(t.Assignee)
	t.Wait = sanitizeWait(t.Wait)
	t.References = sanitizeReferences(t.References)
	t.Description = Sanitize(t.Description)
	if t.Relationships != nil {
		rels := make(map[string][]bundle.TaskRef, len(t.Relationships))
		for kind, refs := range t.Relationships {
			out := make([]bundle.TaskRef, len(refs))
			for i, r := range refs {
				out[i] = bundle.TaskRef{ID: SanitizeLine(r.ID), Title: SanitizeLine(r.Title)}
			}
			rels[SanitizeLine(kind)] = out
		}
		t.Relationships = rels
	}
	if t.Attachments != nil {
		atts := make([]*task.Attachment, len(t.Attachments))
		for i, a := range t.Attachments {
			if a == nil {
				continue
			}
			c := *a
			c.File = SanitizeLine(a.File)
			c.Mime = SanitizeLine(a.Mime)
			c.Caption = Sanitize(a.Caption)
			c.AddedBy = SanitizeLine(a.AddedBy)
			c.AddedAt = SanitizeLine(a.AddedAt)
			atts[i] = &c
		}
		t.Attachments = atts
	}
	if t.Activity != nil {
		activity := make([]bundle.ActivityView, len(t.Activity))
		for i, item := range t.Activity {
			item.At = SanitizeLine(item.At)
			item.Kind = SanitizeLine(item.Kind)
			item.Type = SanitizeLine(item.Type)
			item.Actor = SanitizeLine(item.Actor)
			item.Session = SanitizeLine(item.Session)
			item.Body = Sanitize(item.Body)
			item.Data = sanitizeData(item.Data)
			activity[i] = item
		}
		t.Activity = activity
	}
	return t
}

// sanitizeData deep-copies event data, sanitising every string key and
// value. Summaries quote event data (titles, reasons, captions) verbatim.
func sanitizeData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data))
	for k, v := range maps.All(data) {
		out[SanitizeLine(k)] = sanitizeValue(v)
	}
	return out
}

func sanitizeValue(v any) any {
	switch v := v.(type) {
	case string:
		return Sanitize(v)
	case []string:
		return sanitizeLines(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeValue(item)
		}
		return out
	case map[string]any:
		return sanitizeData(v)
	}
	return v
}
