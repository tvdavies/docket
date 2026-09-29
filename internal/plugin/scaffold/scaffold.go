// Package scaffold writes a new, valid plugin directory from embedded
// templates (`docket plugin new`).
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/tvdavies/docket/internal/plugin"
)

//go:embed all:templates
var templates embed.FS

// Options chooses what the new plugin contains. With no view selected it gets
// a widget, a panel and a page.
type Options struct {
	Name        string
	Description string
	Widget      bool
	Panel       bool
	Page        bool
	Service     bool
	// Port is the service port; zero derives a stable one from the name.
	Port int
	// MinVersion is written as requires.docket; empty or "dev" writes 0.1.0.
	MinVersion string
}

type data struct {
	Options
	Title        string
	Capabilities string
}

// Create writes the plugin into dir, which must not exist or be empty, and
// returns the relative paths written.
func Create(dir string, options Options) ([]string, error) {
	if !validName(options.Name) {
		return nil, fmt.Errorf("plugin name %q must be lowercase letters, numbers, hyphens and underscores", options.Name)
	}
	if !options.Widget && !options.Panel && !options.Page {
		options.Widget, options.Panel, options.Page = true, true, true
	}
	if options.Description == "" {
		options.Description = "A Docket plugin."
	}
	if options.Port == 0 {
		hash := fnv.New32a()
		hash.Write([]byte(options.Name))
		options.Port = 17000 + int(hash.Sum32()%1000)
	}
	if options.MinVersion == "" || options.MinVersion == "dev" {
		options.MinVersion = "0.1.0"
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s already exists and is not empty", dir)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	capabilities := []string{"task.read", "task.comment"}
	if options.Service {
		capabilities = append(capabilities, "service.fetch")
	}
	values := data{Options: options, Title: title(options.Name), Capabilities: strings.Join(capabilities, ", ")}

	var written []string
	err := fs.WalkDir(templates, "templates", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative := strings.TrimSuffix(strings.TrimPrefix(name, "templates/"), ".tmpl")
		mode := fs.FileMode(0o644)
		switch {
		case relative == "bin/plugin":
			if !options.Widget {
				return nil
			}
			relative, mode = "bin/"+options.Name, 0o755
		case strings.HasPrefix(relative, "server/") && !options.Service,
			relative == "ui/widget.html" && !options.Widget,
			relative == "ui/panel.html" && !options.Panel,
			relative == "ui/page.html" && !options.Page:
			return nil
		}
		source, err := templates.ReadFile(name)
		if err != nil {
			return err
		}
		parsed, err := template.New(relative).Parse(string(source))
		if err != nil {
			return err
		}
		var output bytes.Buffer
		if err := parsed.Execute(&output, values); err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, output.Bytes(), mode); err != nil {
			return err
		}
		written = append(written, relative)
		return nil
	})
	if err != nil {
		return written, err
	}
	if _, err := plugin.Load(dir, ""); err != nil {
		return written, fmt.Errorf("scaffolded manifest is invalid (please report this): %w", err)
	}
	return written, nil
}

func validName(name string) bool {
	if name == "" || !(name[0] >= 'a' && name[0] <= 'z' || name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func title(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for index, word := range words {
		words[index] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
