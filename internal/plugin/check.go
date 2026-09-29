package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Problems reports files a valid manifest references but the plugin directory
// does not provide. Load only checks the manifest itself; these checks catch a
// plugin that would install cleanly and then fail at runtime.
func (m *Manifest) Problems() []string {
	var problems []string
	file := func(field, relative string, executable bool) {
		path := filepath.Join(m.Root, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %s does not exist", field, relative))
		case info.IsDir():
			problems = append(problems, fmt.Sprintf("%s: %s is a directory", field, relative))
		case executable && info.Mode()&0o111 == 0:
			problems = append(problems, fmt.Sprintf("%s: %s is not executable", field, relative))
		}
	}
	names := make([]string, 0, len(m.Handlers))
	for name := range m.Handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		handler := m.Handlers[name]
		if handler.Run != "" {
			file("handlers."+name+".run", handler.Run, true)
		} else {
			file("handlers."+name+".lua", handler.Lua, false)
		}
	}
	if m.CLI != nil {
		file("cli.run", m.CLI.Run, true)
	}
	if m.Service != nil && len(m.Service.Command) > 0 {
		program := m.Service.Command[0]
		if strings.Contains(program, "/") {
			file("service.command[0]", program, true)
		} else if _, err := exec.LookPath(program); err != nil {
			problems = append(problems, fmt.Sprintf("service.command[0]: %s is not on PATH", program))
		}
	}
	if m.UI.Dir != "" {
		if info, err := os.Stat(m.UIDir()); err != nil || !info.IsDir() {
			problems = append(problems, fmt.Sprintf("ui.dir: %s is not a directory", m.UI.Dir))
			return problems
		}
		entry := func(field, relative string) {
			if relative != "" {
				file(field, filepath.ToSlash(filepath.Join(m.UI.Dir, relative)), false)
			}
		}
		for index, widget := range m.UI.Widgets {
			entry(fmt.Sprintf("ui.widgets[%d].entry", index), widget.Entry)
		}
		for index, panel := range m.UI.Panels {
			entry(fmt.Sprintf("ui.panels[%d].entry", index), panel.Entry)
		}
		for index, page := range m.UI.Pages {
			entry(fmt.Sprintf("ui.pages[%d].entry", index), page.Entry)
		}
	}
	return problems
}
