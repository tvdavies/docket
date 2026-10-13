package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
)

type fakeSource struct {
	snap     Snapshot
	moved    [][2]string
	comments [][2]string
}

func (f *fakeSource) Load() (Snapshot, error) { return f.snap, nil }

func (f *fakeSource) Detail(id string) (*bundle.Bundle, error) {
	for _, t := range f.snap.Tasks {
		if t.ID == id {
			return &bundle.Bundle{
				ID: t.ID, Title: t.Title, Status: t.Status, Labels: t.Labels,
				Description: "A **markdown** description for " + t.ID,
				CreatedAt:   "2026-10-01T10:00:00Z", UpdatedAt: "2026-10-02T10:00:00Z",
				Activity: []bundle.ActivityView{
					{At: "2026-10-01T10:00:00Z", Kind: "event", Type: "task.created", Actor: "alice"},
					{At: "2026-10-01T11:00:00Z", Kind: "event", Type: "task.moved", Actor: "alice", Data: map[string]any{"from": "backlog", "to": t.Status}},
					{At: "2026-10-02T09:00:00Z", Kind: "comment", Type: "comment", Actor: "bob", Body: "Looks good to me"},
				},
			}, nil
		}
	}
	return nil, nil
}

func (f *fakeSource) Move(id, status string) (string, error) {
	f.moved = append(f.moved, [2]string{id, status})
	for _, t := range f.snap.Tasks {
		if t.ID == id {
			t.Status = status
		}
	}
	return "", nil
}

func (f *fakeSource) Comment(id, text string) (string, error) {
	f.comments = append(f.comments, [2]string{id, text})
	return "", nil
}

func newFake() *fakeSource {
	return &fakeSource{snap: Snapshot{
		Name:     "demo",
		Statuses: []string{"backlog", "in-progress", "done"},
		Tasks: []*task.Task{
			{ID: "TASK-0001", Title: "Fix login cache", Status: "in-progress", Labels: []string{"bug"}, Wait: &task.Wait{Kind: "review"}},
			{ID: "TASK-0002", Title: "Write docs", Status: "backlog"},
			{ID: "TASK-0003", Title: "Plan release", Status: "backlog", Labels: []string{"ops"}},
			{ID: "TASK-0004", Title: "Ship it", Status: "done"},
			{ID: "TASK-0005", Title: "Legacy lane", Status: "archived"},
		},
	}}
}

// drive runs cmd (and any batch it returns), feeding results back into the
// model the way the Bubble Tea runtime would. Timer commands such as cursor
// blinks are abandoned rather than awaited.
func drive(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for depth := 0; cmd != nil && depth < 10; depth++ {
		result := make(chan tea.Msg, 1)
		go func(cmd tea.Cmd) { result <- cmd() }(cmd)
		var msg tea.Msg
		select {
		case msg = <-result:
		case <-time.After(50 * time.Millisecond):
			return m
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				m = drive(t, m, c)
			}
			return m
		case nil:
			return m
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			return m
		}
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(Model)
	}
	return m
}

func send(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = send(t, m, keyMsg(k))
		m = drive(t, m, cmd)
	}
	return m
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func loaded(t *testing.T, src Source, width, height int) Model {
	t.Helper()
	m := New(src)
	m.now = func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC) }
	m, _ = send(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	return drive(t, m, m.load())
}

func screen(m Model) string { return xansi.Strip(m.render()) }

func TestBoardGroupsTasksIntoConfiguredLanesInOrder(t *testing.T) {
	m := loaded(t, newFake(), 140, 30)
	var statuses []string
	for _, c := range m.columns {
		statuses = append(statuses, c.status)
	}
	if got := strings.Join(statuses, ","); got != "backlog,in-progress,done,archived" {
		t.Fatalf("lanes = %s", got)
	}
	out := screen(m)
	for _, want := range []string{"● backlog 2", "● in-progress 1", "TASK-0001", "Fix login cache", "◷ review", "#bug", "demo · 5 tasks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("board missing %q:\n%s", want, out)
		}
	}
}

func TestBoardNavigationMovesBetweenLanesAndCards(t *testing.T) {
	m := loaded(t, newFake(), 140, 30)
	if got := m.selectedID(); got != "TASK-0002" {
		t.Fatalf("initial selection = %s", got)
	}
	m = press(t, m, "j")
	if got := m.selectedID(); got != "TASK-0003" {
		t.Fatalf("after j = %s", got)
	}
	m = press(t, m, "j") // clamps at the bottom
	if got := m.selectedID(); got != "TASK-0003" {
		t.Fatalf("after second j = %s", got)
	}
	m = press(t, m, "l")
	if got := m.selectedID(); got != "TASK-0001" {
		t.Fatalf("after l = %s", got)
	}
	m = press(t, m, "h")
	if got := m.selectedID(); got != "TASK-0003" {
		t.Fatalf("lane remembers its row: after h = %s", got)
	}
	m = press(t, m, "right", "tab")
	if got := m.selectedID(); got != "TASK-0004" {
		t.Fatalf("after right, tab = %s", got)
	}
	m = press(t, m, "tab", "tab") // wraps past the last lane
	if m.col != 0 {
		t.Fatalf("tab did not wrap: col = %d", m.col)
	}
	m = press(t, m, "shift+tab")
	if m.col != 3 {
		t.Fatalf("shift+tab did not wrap backwards: col = %d", m.col)
	}
}

func TestFilterNarrowsCardsAndEscClearsIt(t *testing.T) {
	m := loaded(t, newFake(), 140, 30)
	m = press(t, m, "/")
	if m.mode != modeFilter {
		t.Fatalf("mode = %v", m.mode)
	}
	m = press(t, m, "o", "p", "s")
	if m.query != "ops" {
		t.Fatalf("query = %q", m.query)
	}
	total := 0
	for _, c := range m.columns {
		total += len(c.tasks)
	}
	if total != 1 || m.selectedID() != "TASK-0003" {
		t.Fatalf("filtered to %d tasks, selected %s", total, m.selectedID())
	}
	m = press(t, m, "enter")
	if m.mode != modeBoard || m.query != "ops" {
		t.Fatalf("enter should keep the filter: mode=%v query=%q", m.mode, m.query)
	}
	if out := screen(m); !strings.Contains(out, `filter "ops" · 1 of 5 tasks`) {
		t.Fatalf("missing filter banner:\n%s", out)
	}
	m = press(t, m, "esc")
	if m.query != "" {
		t.Fatalf("esc did not clear the filter: %q", m.query)
	}
	if m.selectedID() != "TASK-0003" {
		t.Fatalf("selection lost after clearing filter: %s", m.selectedID())
	}
}

func TestEnterOpensDetailWithTabsAndEscReturns(t *testing.T) {
	m := loaded(t, newFake(), 120, 40)
	m = press(t, m, "l", "enter")
	if m.mode != modeDetail || m.detail == nil || m.detail.ID != "TASK-0001" {
		t.Fatalf("detail not open: mode=%v detail=%v", m.mode, m.detail)
	}
	out := screen(m)
	for _, want := range []string{"TASK-0001", "● in-progress", "Fix login cache", "1 Overview", "2 Comments (1)", "3 Activity (3)", "markdown description"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail missing %q:\n%s", want, out)
		}
	}
	m = press(t, m, "tab", "tab")
	if m.tab != tabActivity {
		t.Fatalf("tab = %d", m.tab)
	}
	out = screen(m)
	for _, want := range []string{"alice created the task", "alice moved backlog → in-progress", "bob commented", "Looks good to me"} {
		if !strings.Contains(out, want) {
			t.Fatalf("activity missing %q:\n%s", want, out)
		}
	}
	m = press(t, m, "2")
	if m.tab != tabComments {
		t.Fatalf("number key did not select comments: %d", m.tab)
	}
	m = press(t, m, "esc")
	if m.mode != modeBoard || m.detailID != "" {
		t.Fatalf("esc did not return to the board: mode=%v", m.mode)
	}
	if m.selectedID() != "TASK-0001" {
		t.Fatalf("selection changed: %s", m.selectedID())
	}
}

func TestQuitKeys(t *testing.T) {
	m := loaded(t, newFake(), 120, 40)
	for _, k := range []string{"q", "esc"} {
		_, cmd := send(t, m, keyMsg(k))
		if cmd == nil {
			t.Fatalf("%s returned no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s did not quit", k)
		}
	}
}

func TestMovePickerMovesThroughSource(t *testing.T) {
	src := newFake()
	m := loaded(t, src, 120, 40)
	m = press(t, m, "m")
	if m.mode != modeMove || m.moveTo != 0 {
		t.Fatalf("picker: mode=%v cursor=%d", m.mode, m.moveTo)
	}
	m = press(t, m, "j", "enter")
	if len(src.moved) != 1 || src.moved[0] != [2]string{"TASK-0002", "in-progress"} {
		t.Fatalf("moved = %v", src.moved)
	}
	if m.mode != modeBoard || !strings.Contains(m.status, "Moved TASK-0002 to in-progress") {
		t.Fatalf("mode=%v status=%q", m.mode, m.status)
	}
	if m.selectedID() != "TASK-0002" || m.columns[m.col].status != "in-progress" {
		t.Fatalf("selection did not follow the moved card: %s in %s", m.selectedID(), m.columns[m.col].status)
	}
}

func TestCommentFromDetailUsesSource(t *testing.T) {
	src := newFake()
	m := loaded(t, src, 120, 40)
	m = press(t, m, "enter", "c")
	if m.mode != modeComment {
		t.Fatalf("mode = %v", m.mode)
	}
	m = press(t, m, "h", "i", "ctrl+s")
	if len(src.comments) != 1 || src.comments[0] != [2]string{"TASK-0002", "hi"} {
		t.Fatalf("comments = %v", src.comments)
	}
	if m.mode != modeDetail {
		t.Fatalf("comment should return to detail, mode = %v", m.mode)
	}
}

func TestNarrowTerminalScrollsLanesToKeepSelectionVisible(t *testing.T) {
	m := loaded(t, newFake(), 40, 30)
	first, count, _ := m.visibleColumns()
	if count != 1 || first != 0 {
		t.Fatalf("first=%d count=%d", first, count)
	}
	m = press(t, m, "l", "l")
	out := screen(m)
	if !strings.Contains(out, "● done") || strings.Contains(out, "● backlog") {
		t.Fatalf("selected lane not shown alone:\n%s", out)
	}
	if !strings.Contains(out, "‹ 2") || !strings.Contains(out, "1 › lanes") {
		t.Fatalf("missing scroll hints:\n%s", out)
	}
}

func TestRefreshMessageReloadsSnapshot(t *testing.T) {
	src := newFake()
	m := loaded(t, src, 120, 40)
	src.snap.Tasks = append(src.snap.Tasks, &task.Task{ID: "TASK-0006", Title: "New arrival", Status: "done"})
	m, cmd := send(t, m, RefreshMsg{})
	m = drive(t, m, cmd)
	if out := screen(m); !strings.Contains(out, "New arrival") {
		t.Fatalf("refresh did not reload:\n%s", out)
	}
}
