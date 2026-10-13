package tui

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/ui"
)

type mode int

const (
	modeBoard mode = iota
	modeDetail
	modeFilter
	modeMove
	modeComment
)

// Detail view tabs.
const (
	tabOverview = iota
	tabComments
	tabActivity
	tabLinks
	tabCount
)

var tabNames = [tabCount]string{"Overview", "Comments", "Activity", "Links"}

type column struct {
	status string
	tasks  []*task.Task
}

// Messages.
type (
	snapshotMsg struct {
		snap Snapshot
		err  error
	}
	detailMsg struct {
		id     string
		bundle *bundle.Bundle
		err    error
	}
	actionMsg struct {
		note string
		err  error
		done string
	}
	// RefreshMsg asks the board to reload, for example after the workspace
	// changed on disk.
	RefreshMsg struct{}
)

// Model is the board's Bubble Tea model.
type Model struct {
	src   Source
	keys  keyMap
	help  help.Model
	theme ui.Theme
	now   func() time.Time

	width, height int

	snap    Snapshot
	loaded  bool
	loadErr error
	columns []column

	col  int
	rows map[string]int

	mode     mode
	overlay  mode // board or detail: where move/comment return to
	filter   textinput.Model
	query    string
	comment  textarea.Model
	moveTo   int
	status   string
	statusOK bool

	detailID string
	detail   *bundle.Bundle
	tab      int
	viewport viewport.Model
}

// New creates a board model reading from src.
func New(src Source) Model {
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter by id, title, label, assignee"
	comment := textarea.New()
	comment.Placeholder = "Write a comment (markdown). ctrl+s saves, esc cancels."
	comment.ShowLineNumbers = false
	return Model{
		src:      src,
		keys:     newKeyMap(),
		help:     help.New(),
		theme:    ui.NewTheme(true),
		now:      time.Now,
		rows:     map[string]int{},
		filter:   filter,
		comment:  comment,
		viewport: viewport.New(),
	}
}

// Init loads the board and asks the terminal for its background colour.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), tea.RequestBackgroundColor)
}

func (m Model) load() tea.Cmd {
	src := m.src
	return func() tea.Msg {
		snap, err := src.Load()
		return snapshotMsg{snap: snap, err: err}
	}
}

func (m Model) loadDetail(id string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		b, err := src.Detail(id)
		return detailMsg{id: id, bundle: b, err: err}
	}
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.comment.SetWidth(max(msg.Width-6, 20))
		m.comment.SetHeight(max(msg.Height/3, 5))
		m.filter.SetWidth(max(msg.Width-4, 10))
		m.layoutDetail()
		return m, nil
	case tea.BackgroundColorMsg:
		m.theme = ui.NewTheme(msg.IsDark())
		m.help.Styles = help.DefaultStyles(msg.IsDark())
		m.layoutDetail()
		return m, nil
	case snapshotMsg:
		if msg.err != nil {
			m.loadErr = msg.err
			return m, nil
		}
		m.loadErr = nil
		selected := m.selectedID()
		m.snap, m.loaded = msg.snap, true
		m.rebuild(selected)
		if m.detailID != "" {
			return m, m.loadDetail(m.detailID)
		}
		return m, nil
	case detailMsg:
		if msg.id != m.detailID {
			return m, nil
		}
		if msg.err != nil {
			m.setStatus(msg.err.Error(), false)
			if m.detail == nil {
				m.closeDetail()
			}
			return m, nil
		}
		offset := m.viewport.YOffset()
		reload := m.detail != nil && m.detail.ID == msg.bundle.ID
		m.detail = msg.bundle
		m.layoutDetail()
		if reload {
			m.viewport.SetYOffset(offset)
		}
		return m, nil
	case actionMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), false)
		} else {
			text := msg.done
			if msg.note != "" {
				text += " · " + msg.note
			}
			m.setStatus(text, true)
		}
		return m, m.load()
	case RefreshMsg:
		return m, m.load()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m.forward(msg)
}

// forward passes non-key messages (cursor blinks, pastes) to the focused
// component.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.mode {
	case modeFilter:
		m.filter, cmd = m.filter.Update(msg)
	case modeComment:
		m.comment, cmd = m.comment.Update(msg)
	case modeDetail:
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m *Model) setStatus(text string, ok bool) {
	m.status, m.statusOK = text, ok
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.mode != modeComment && m.mode != modeFilter {
		m.status = ""
	}
	switch m.mode {
	case modeFilter:
		return m.filterKey(msg)
	case modeMove:
		return m.moveKey(msg)
	case modeComment:
		return m.commentKey(msg)
	case modeDetail:
		return m.detailKey(msg)
	}
	return m.boardKey(msg)
}

func (m Model) boardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case msg.String() == "esc":
		if m.query != "" {
			m.query = ""
			m.filter.SetValue("")
			m.rebuild(m.selectedID())
			return m, nil
		}
		return m, tea.Quit
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
	case key.Matches(msg, k.Left):
		m.moveColumn(-1, false)
	case key.Matches(msg, k.Right):
		m.moveColumn(1, false)
	case key.Matches(msg, k.NextColumn):
		m.moveColumn(1, true)
	case key.Matches(msg, k.PrevColumn):
		m.moveColumn(-1, true)
	case key.Matches(msg, k.Up):
		m.moveRow(-1)
	case key.Matches(msg, k.Down):
		m.moveRow(1)
	case key.Matches(msg, k.Top):
		m.moveRow(-1 << 30)
	case key.Matches(msg, k.Bottom):
		m.moveRow(1 << 30)
	case key.Matches(msg, k.Open):
		if t := m.selectedTask(); t != nil {
			m.mode, m.detailID, m.detail, m.tab = modeDetail, t.ID, nil, tabOverview
			m.viewport.GotoTop()
			return m, m.loadDetail(t.ID)
		}
	case key.Matches(msg, k.Filter):
		m.mode = modeFilter
		m.filter.SetValue(m.query)
		m.filter.CursorEnd()
		return m, m.filter.Focus()
	case key.Matches(msg, k.Refresh):
		m.setStatus("Reloaded", true)
		return m, m.load()
	case key.Matches(msg, k.Move):
		return m.startMove()
	case key.Matches(msg, k.Comment):
		return m.startComment()
	}
	return m, nil
}

func (m Model) detailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Back):
		m.closeDetail()
		return m, nil
	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layoutDetail()
	case key.Matches(msg, k.NextTab):
		m.tab = (m.tab + 1) % tabCount
		m.layoutDetail()
		m.viewport.GotoTop()
	case key.Matches(msg, k.PrevTab):
		m.tab = (m.tab + tabCount - 1) % tabCount
		m.layoutDetail()
		m.viewport.GotoTop()
	case msg.String() >= "1" && msg.String() <= "4" && len(msg.String()) == 1:
		m.tab = int(msg.String()[0] - '1')
		m.layoutDetail()
		m.viewport.GotoTop()
	case key.Matches(msg, k.Top):
		m.viewport.GotoTop()
	case key.Matches(msg, k.Bottom):
		m.viewport.GotoBottom()
	case key.Matches(msg, k.Refresh):
		m.setStatus("Reloaded", true)
		return m, m.load()
	case key.Matches(msg, k.Move):
		return m.startMove()
	case key.Matches(msg, k.Comment):
		return m.startComment()
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) closeDetail() {
	m.mode, m.detailID, m.detail = modeBoard, "", nil
}

func (m Model) filterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.ClearFilter):
		m.query = ""
		m.filter.SetValue("")
		m.filter.Blur()
		m.mode = modeBoard
		m.rebuild(m.selectedID())
		return m, nil
	case key.Matches(msg, m.keys.AcceptFilter):
		m.filter.Blur()
		m.mode = modeBoard
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	if value := m.filter.Value(); value != m.query {
		m.query = value
		m.rebuild(m.selectedID())
	}
	return m, cmd
}

// actionTarget is the task move and comment act on.
func (m Model) actionTarget() string {
	if m.detailID != "" {
		return m.detailID
	}
	if t := m.selectedTask(); t != nil {
		return t.ID
	}
	return ""
}

func (m Model) startMove() (tea.Model, tea.Cmd) {
	id := m.actionTarget()
	if id == "" || len(m.snap.Statuses) == 0 {
		return m, nil
	}
	m.overlay = m.mode
	m.mode = modeMove
	m.moveTo = 0
	if t := m.taskByID(id); t != nil {
		if i := slices.Index(m.snap.Statuses, t.Status); i >= 0 {
			m.moveTo = i
		}
	}
	return m, nil
}

func (m Model) moveKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Cancel), msg.String() == "q":
		m.mode = m.overlay
	case key.Matches(msg, k.Up):
		m.moveTo = max(m.moveTo-1, 0)
	case key.Matches(msg, k.Down):
		m.moveTo = min(m.moveTo+1, len(m.snap.Statuses)-1)
	case key.Matches(msg, k.Confirm):
		id, status := m.actionTarget(), m.snap.Statuses[m.moveTo]
		m.mode = m.overlay
		if t := m.taskByID(id); t != nil && t.Status == status {
			return m, nil
		}
		src := m.src
		return m, func() tea.Msg {
			note, err := src.Move(id, status)
			return actionMsg{note: note, err: err, done: "Moved " + id + " to " + status}
		}
	}
	return m, nil
}

func (m Model) startComment() (tea.Model, tea.Cmd) {
	if m.actionTarget() == "" {
		return m, nil
	}
	m.overlay = m.mode
	m.mode = modeComment
	m.comment.Reset()
	return m, m.comment.Focus()
}

func (m Model) commentKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.comment.Blur()
		m.mode = m.overlay
		return m, nil
	case key.Matches(msg, m.keys.Submit):
		text := strings.TrimSpace(m.comment.Value())
		if text == "" {
			m.setStatus("Comment is empty", false)
			return m, nil
		}
		id := m.actionTarget()
		m.comment.Blur()
		m.mode = m.overlay
		src := m.src
		return m, func() tea.Msg {
			note, err := src.Comment(id, text)
			return actionMsg{note: note, err: err, done: "Commented on " + id}
		}
	}
	var cmd tea.Cmd
	m.comment, cmd = m.comment.Update(msg)
	return m, cmd
}

// rebuild regroups tasks into lanes applying the filter, keeping the
// selection on the same task when it is still visible.
func (m *Model) rebuild(selected string) {
	query := strings.ToLower(strings.TrimSpace(m.query))
	lanes := map[string]*column{}
	var columns []column
	for _, status := range m.snap.Statuses {
		if _, ok := lanes[status]; !ok {
			columns = append(columns, column{status: status})
			lanes[status] = nil
		}
	}
	var extra []string
	for _, t := range m.snap.Tasks {
		if _, ok := lanes[t.Status]; !ok {
			lanes[t.Status] = nil
			extra = append(extra, t.Status)
		}
	}
	slices.Sort(extra)
	for _, status := range extra {
		columns = append(columns, column{status: status})
	}
	index := map[string]int{}
	for i, c := range columns {
		index[c.status] = i
	}
	for _, t := range m.snap.Tasks {
		if query != "" && !matches(t, query) {
			continue
		}
		i := index[t.Status]
		columns[i].tasks = append(columns[i].tasks, t)
	}
	m.columns = columns
	if selected != "" {
		for ci, c := range columns {
			for ri, t := range c.tasks {
				if t.ID == selected {
					m.col = ci
					m.rows[c.status] = ri
				}
			}
		}
	}
	m.col = clamp(m.col, 0, len(columns)-1)
	for _, c := range columns {
		m.rows[c.status] = clamp(m.rows[c.status], 0, len(c.tasks)-1)
	}
}

func matches(t *task.Task, query string) bool {
	fields := append([]string{t.ID, t.Title, t.Assignee, t.Status}, t.Labels...)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func (m *Model) moveColumn(delta int, wrap bool) {
	if len(m.columns) == 0 {
		return
	}
	next := m.col + delta
	if wrap {
		next = (next + len(m.columns)) % len(m.columns)
	}
	m.col = clamp(next, 0, len(m.columns)-1)
}

func (m *Model) moveRow(delta int) {
	if len(m.columns) == 0 {
		return
	}
	c := m.columns[m.col]
	m.rows[c.status] = clamp(m.rows[c.status]+delta, 0, len(c.tasks)-1)
}

func (m Model) selectedTask() *task.Task {
	if m.col < 0 || m.col >= len(m.columns) {
		return nil
	}
	c := m.columns[m.col]
	row := m.rows[c.status]
	if row < 0 || row >= len(c.tasks) {
		return nil
	}
	return c.tasks[row]
}

func (m Model) selectedID() string {
	if t := m.selectedTask(); t != nil {
		return t.ID
	}
	return ""
}

func (m Model) taskByID(id string) *task.Task {
	for _, t := range m.snap.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}
