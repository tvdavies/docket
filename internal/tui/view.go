package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/ui"
)

const (
	minColumnWidth = 28
	cardHeight     = 4 // three lines plus a gap
	maxDetailWidth = 110
)

// View renders the current screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "docket"
	return v
}

func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if !m.loaded {
		if m.loadErr != nil {
			return m.theme.Fg(m.theme.Danger).Render("Could not load the board: "+ui.SanitizeLine(m.loadErr.Error())) + "\n\n" + m.theme.Dim("press r to retry or q to quit")
		}
		return m.theme.Dim("Loading…")
	}
	footer := m.footer()
	bodyHeight := max(m.height-lipgloss.Height(footer), 1)
	var body string
	switch {
	case m.mode == modeMove:
		body = lipgloss.Place(m.width, bodyHeight, lipgloss.Center, lipgloss.Center, m.movePicker())
	case m.mode == modeComment:
		body = lipgloss.Place(m.width, bodyHeight, lipgloss.Center, lipgloss.Center, m.commentBox())
	case m.mode == modeDetail:
		body = m.detailView()
	default:
		body = m.boardView(bodyHeight)
	}
	body = lipgloss.NewStyle().Height(bodyHeight).MaxHeight(bodyHeight).Render(body)
	return body + "\n" + footer
}

func (m Model) footer() string {
	var lines []string
	if m.mode == modeFilter {
		lines = append(lines, m.filter.View())
	}
	if m.status != "" {
		color := m.theme.Good
		if !m.statusOK {
			color = m.theme.Danger
		}
		lines = append(lines, m.theme.Fg(color).Render(ui.Truncate(ui.SanitizeLine(m.status), m.width)))
	} else if m.loadErr != nil {
		lines = append(lines, m.theme.Fg(m.theme.Danger).Render(ui.Truncate("reload failed: "+ui.SanitizeLine(m.loadErr.Error()), m.width)))
	}
	lines = append(lines, m.help.View(m.keys.help(m.mode)))
	return strings.Join(lines, "\n")
}

// visibleColumns returns the first visible lane, how many lanes fit, and
// each lane's width. Narrow terminals scroll lanes horizontally so the
// selected lane is always on screen.
func (m Model) visibleColumns() (first, count, width int) {
	n := len(m.columns)
	if n == 0 {
		return 0, 0, m.width
	}
	count = clamp(m.width/minColumnWidth, 1, n)
	width = m.width / count
	first = clamp(m.colOffsetFor(count), 0, n-count)
	return first, count, width
}

// colOffsetFor keeps the selected lane centred-ish in view.
func (m Model) colOffsetFor(count int) int {
	return m.col - count/2
}

func (m Model) boardView(height int) string {
	th := m.theme
	first, count, width := m.visibleColumns()

	title := th.Fg(th.Accent).Bold(true).Render("docket")
	if m.snap.Name != "" {
		title += th.Dim(" · " + ui.SanitizeLine(m.snap.Name))
	}
	shown, total := 0, len(m.snap.Tasks)
	for _, c := range m.columns {
		shown += len(c.tasks)
	}
	if m.query != "" {
		title += "  " + th.Fg(th.Warn).Render(fmt.Sprintf("filter %q · %d of %d tasks", m.query, shown, total))
	} else {
		title += th.Dim(fmt.Sprintf(" · %d tasks", total))
	}
	var scroll []string
	if first > 0 {
		scroll = append(scroll, fmt.Sprintf("‹ %d", first))
	}
	if rest := len(m.columns) - first - count; rest > 0 {
		scroll = append(scroll, fmt.Sprintf("%d ›", rest))
	}
	if len(scroll) > 0 {
		hint := th.Dim(strings.Join(scroll, "  ") + " lanes")
		gap := m.width - lipgloss.Width(title) - lipgloss.Width(hint)
		title += strings.Repeat(" ", max(gap, 2)) + hint
	}

	if len(m.columns) == 0 {
		return title + "\n\n" + th.Dim("No status lanes are configured.")
	}
	cardsHeight := max(height-4, cardHeight) // title, blank, lane header (2)
	var lanes []string
	for i := first; i < first+count; i++ {
		lanes = append(lanes, m.laneView(i, width-1, cardsHeight))
	}
	return title + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, lanes...)
}

func (m Model) laneView(index, width, height int) string {
	th := m.theme
	c := m.columns[index]
	active := index == m.col
	color := th.StatusColor(c.status)
	row := m.rows[c.status]

	perPage := max(height/cardHeight, 1)
	offset := clamp(row-perPage+1, 0, max(len(c.tasks)-perPage, 0))
	if row < offset {
		offset = row
	}

	count := fmt.Sprintf(" %d", len(c.tasks))
	if len(c.tasks) > perPage {
		count = fmt.Sprintf(" %d/%d", row+1, len(c.tasks))
	}
	headerStyle := th.Fg(color).Bold(true)
	header := headerStyle.Render(ui.Truncate("● "+ui.SanitizeLine(c.status), width-lipgloss.Width(count))) + th.Dim(count)
	ruleChar := "─"
	if active {
		ruleChar = "━"
	}
	rule := th.Fg(color).Render(strings.Repeat(ruleChar, max(width-1, 1)))

	lines := []string{header, rule}
	if len(c.tasks) == 0 {
		lines = append(lines, "", th.Dim("  no tasks"))
	}
	end := min(offset+perPage, len(c.tasks))
	for i := offset; i < end; i++ {
		lines = append(lines, m.card(c.tasks[i], width-1, active && i == row))
	}
	if end < len(c.tasks) {
		lines = append(lines, th.Dim(fmt.Sprintf("  ↓ %d more", len(c.tasks)-end)))
	}
	return lipgloss.NewStyle().Width(width + 1).PaddingRight(1).Render(strings.Join(lines, "\n"))
}

func (m Model) card(t *task.Task, width int, selected bool) string {
	th := m.theme
	// Draw a sanitised copy; actions keep using the raw task.
	t = ui.SanitizeTask(t)
	inner := max(width-2, 4) // border + padding
	id := th.Fg(th.Muted).Render(t.ID)
	if t.Wait != nil {
		wait := ui.Truncate("◷ "+t.Wait.Kind, max(inner-lipgloss.Width(t.ID)-1, 1))
		gap := inner - lipgloss.Width(t.ID) - lipgloss.Width(wait)
		id += strings.Repeat(" ", max(gap, 1)) + th.Fg(th.Warn).Render(wait)
	}
	titleStyle := lipgloss.NewStyle().Foreground(th.Text)
	if selected {
		titleStyle = titleStyle.Bold(true)
	}
	title := titleStyle.Render(ui.Truncate(strings.Join(strings.Fields(t.Title), " "), inner))
	var meta []string
	if len(t.Labels) > 0 {
		meta = append(meta, "#"+strings.Join(t.Labels, " #"))
	}
	if t.Assignee != "" {
		meta = append(meta, "@"+t.Assignee)
	}
	third := th.Fg(th.Accent).Render(ui.Truncate(strings.Join(meta, "  "), inner))

	border := th.Subtle
	if selected {
		border = th.Accent
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderForeground(border).
		PaddingLeft(1).
		Width(width)
	if selected {
		style = style.Background(th.Select)
	}
	return style.Render(id+"\n"+title+"\n"+third) + "\n"
}

func (m Model) movePicker() string {
	th := m.theme
	var lines []string
	lines = append(lines, th.Bold("Move "+ui.SanitizeLine(m.target)+" to"), "")
	current := ""
	if t := m.taskByID(m.target); t != nil {
		current = t.Status
	}
	for i, status := range m.snap.Statuses {
		cursor := "  "
		if i == m.moveTo {
			cursor = th.Fg(th.Accent).Render("› ")
		}
		line := cursor + th.StatusBadge(ui.SanitizeLine(status))
		if status == current {
			line += th.Dim("  (current)")
		}
		lines = append(lines, line)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.Accent).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
}

func (m Model) commentBox() string {
	th := m.theme
	head := th.Bold("Comment on " + ui.SanitizeLine(m.target))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.Accent).
		Padding(0, 1).
		Render(head + "\n\n" + m.comment.View())
}

// renderer is the shared task renderer sized for the detail view.
func (m Model) renderer() ui.Renderer {
	return ui.Renderer{Theme: m.theme, Width: m.detailWidth(), Now: m.now()}
}

func (m Model) detailWidth() int {
	return clamp(m.width-4, 20, maxDetailWidth)
}

// detailChrome renders the fixed header and tab bar above the viewport.
func (m Model) detailChrome() string {
	if m.detail == nil {
		return m.theme.Dim("Loading " + ui.SanitizeLine(m.detailID) + "…")
	}
	value := ui.FromBundle(m.detail)
	header := m.renderer().Header(value)
	return header + "\n\n" + m.tabBar(value)
}

func (m Model) tabBar(value ui.Task) string {
	th := m.theme
	comments := 0
	for _, item := range value.Activity {
		if item.Kind == "comment" {
			comments++
		}
	}
	links := len(value.References) + len(value.Attachments)
	for _, refs := range value.Relationships {
		links += len(refs)
	}
	counts := [tabCount]int{-1, comments, len(value.Activity), links}
	var tabs []string
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if counts[i] >= 0 {
			label += fmt.Sprintf(" (%d)", counts[i])
		}
		style := lipgloss.NewStyle().Padding(0, 1)
		if i == m.tab {
			style = style.Bold(true).Foreground(th.Accent).Underline(true)
		} else {
			style = style.Foreground(th.Muted)
		}
		tabs = append(tabs, style.Render(label))
	}
	return strings.Join(tabs, th.Dim("│"))
}

// detailContent renders the selected tab's body.
func (m Model) detailContent() string {
	if m.detail == nil {
		return ""
	}
	value := ui.FromBundle(m.detail)
	r := m.renderer()
	switch m.tab {
	case tabComments:
		return r.Comments(value)
	case tabActivity:
		if out := r.Timeline(value); out != "" {
			return out
		}
		return m.theme.Dim("No activity yet.")
	case tabLinks:
		if out := r.Links(value); out != "" {
			return out
		}
		return m.theme.Dim("No relationships, references, or attachments.")
	}
	return r.Overview(value)
}

// layoutDetail sizes the viewport to the space left by the chrome and
// footer and fills it with the current tab.
func (m *Model) layoutDetail() {
	if m.detailID == "" || m.width == 0 {
		return
	}
	chrome := m.detailChrome()
	height := m.height - lipgloss.Height(m.footer()) - lipgloss.Height(chrome) - 2
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(max(height, 3))
	content := lipgloss.NewStyle().PaddingLeft(2).Render(m.detailContent())
	m.viewport.SetContent(content)
}

func (m Model) detailView() string {
	chrome := lipgloss.NewStyle().PaddingLeft(2).Render(m.detailChrome())
	scroll := ""
	if m.detail != nil && m.viewport.TotalLineCount() > m.viewport.Height() {
		scroll = m.theme.Dim(fmt.Sprintf("  %3.0f%%", m.viewport.ScrollPercent()*100))
	}
	return chrome + scroll + "\n\n" + m.viewport.View()
}
