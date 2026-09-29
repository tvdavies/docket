package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Problems reports files a valid manifest references but the plugin directory
// does not provide. Load only checks the manifest itself; these checks catch a
// plugin that would install cleanly and then fail at runtime. Legacy ui and
// service.command files are not checked because Docket no longer uses them.
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
	return problems
}
