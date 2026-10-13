package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Left, Right, Up, Down     key.Binding
	NextColumn, PrevColumn    key.Binding
	Top, Bottom               key.Binding
	Open, Back, Quit          key.Binding
	Filter, Refresh, Help     key.Binding
	Move, Comment             key.Binding
	NextTab, PrevTab          key.Binding
	PageUp, PageDown          key.Binding
	Submit, Cancel, Confirm   key.Binding
	ClearFilter, AcceptFilter key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Left:         key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("←/h", "lane left")),
		Right:        key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("→/l", "lane right")),
		Up:           key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("↑/k", "up")),
		Down:         key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("↓/j", "down")),
		NextColumn:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next lane")),
		PrevColumn:   key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous lane")),
		Top:          key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:       key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Open:         key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Back:         key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "back")),
		Quit:         key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit")),
		Filter:       key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Refresh:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Move:         key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "move")),
		Comment:      key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "comment")),
		NextTab:      key.NewBinding(key.WithKeys("tab", "l", "right"), key.WithHelp("tab/→", "next tab")),
		PrevTab:      key.NewBinding(key.WithKeys("shift+tab", "h", "left"), key.WithHelp("shift+tab/←", "previous tab")),
		PageUp:       key.NewBinding(key.WithKeys("pgup", "b", "u"), key.WithHelp("pgup/u", "page up")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown", " ", "d"), key.WithHelp("pgdn/d", "page down")),
		Submit:       key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save")),
		Cancel:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		Confirm:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose")),
		ClearFilter:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear")),
		AcceptFilter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply")),
	}
}

// helpKeys adapts the key map to bubbles/help for one mode.
type helpKeys struct {
	short []key.Binding
	full  [][]key.Binding
}

func (h helpKeys) ShortHelp() []key.Binding  { return h.short }
func (h helpKeys) FullHelp() [][]key.Binding { return h.full }

func (k keyMap) help(m mode) helpKeys {
	switch m {
	case modeDetail:
		return helpKeys{
			short: []key.Binding{k.NextTab, k.Up, k.Down, k.Move, k.Comment, k.Back, k.Help},
			full: [][]key.Binding{
				{k.NextTab, k.PrevTab},
				{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
				{k.Move, k.Comment, k.Refresh},
				{k.Back, k.Help},
			},
		}
	case modeFilter:
		return helpKeys{short: []key.Binding{k.AcceptFilter, k.ClearFilter}}
	case modeMove:
		return helpKeys{short: []key.Binding{k.Up, k.Down, k.Confirm, k.Cancel}}
	case modeComment:
		return helpKeys{short: []key.Binding{k.Submit, k.Cancel}}
	}
	return helpKeys{
		short: []key.Binding{k.Left, k.Right, k.Up, k.Down, k.Open, k.Filter, k.Quit, k.Help},
		full: [][]key.Binding{
			{k.Left, k.Right, k.NextColumn, k.PrevColumn},
			{k.Up, k.Down, k.Top, k.Bottom},
			{k.Open, k.Filter, k.Refresh},
			{k.Move, k.Comment, k.Quit, k.Help},
		},
	}
}
