package workspace

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var handlerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config is the parsed `config.yaml`. It declares the status lanes, advisory
// labels, typed relationship kinds, event handlers, and id-generation settings.
type Config struct {
	// Statuses double as board lanes, in order.
	Statuses []string `yaml:"statuses"`
	// Terminal statuses are treated as "closed" for filtering.
	Terminal []string `yaml:"terminal,omitempty"`
	// Labels are advisory; free-form labels are allowed too.
	Labels []string `yaml:"labels,omitempty"`
	// Relationships declares typed link kinds and their inverses.
	Relationships []RelType `yaml:"relationships"`
	// Handlers are post-hoc event consumers. Each handler has its own durable
	// cursor and receives matching events as JSON lines on stdin.
	Handlers map[string]HandlerConfig `yaml:"handlers,omitempty"`
	Settings Settings                 `yaml:"settings"`
}

// HandlerConfig registers one executable event consumer. Run is resolved
// relative to the project root (the parent of .docket/) unless absolute.
type HandlerConfig struct {
	On  []string `yaml:"on"`
	Run string   `yaml:"run"`
}

// Matches reports whether this handler consumes an event type. "*" matches
// every event; otherwise matches are exact.
func (h HandlerConfig) Matches(eventType string) bool {
	for _, pattern := range h.On {
		if pattern == "*" || pattern == eventType {
			return true
		}
	}
	return false
}

// RelType is a typed relationship kind and its inverse. A symmetric
// relationship (e.g. "relates") names itself as its inverse.
type RelType struct {
	Name    string `yaml:"name"`
	Inverse string `yaml:"inverse"`
}

// Settings controls id generation.
type Settings struct {
	IDPrefix       string `yaml:"id_prefix"`
	IDPadding      int    `yaml:"id_padding"`
	ProjectPrefix  string `yaml:"project_prefix"`
	ProjectPadding int    `yaml:"project_padding"`
}

// DefaultConfig is written by `docket init`.
func DefaultConfig() *Config {
	c := &Config{
		Statuses: []string{"backlog", "ready", "in-progress", "blocked", "in-review", "done"},
		Terminal: []string{"done"},
		Labels:   []string{"bug", "feature", "chore"},
		Relationships: []RelType{
			{Name: "blocks", Inverse: "blocked-by"},
			{Name: "parent", Inverse: "subtasks"},
			{Name: "relates", Inverse: "relates"},
			{Name: "duplicate-of", Inverse: "duplicates"},
		},
	}
	c.applyDefaults()
	return c
}

func (c *Config) applyDefaults() {
	if c.Settings.IDPrefix == "" {
		c.Settings.IDPrefix = "TASK"
	}
	if c.Settings.IDPadding == 0 {
		c.Settings.IDPadding = 4
	}
	if c.Settings.ProjectPrefix == "" {
		c.Settings.ProjectPrefix = "PROJ"
	}
	if c.Settings.ProjectPadding == 0 {
		c.Settings.ProjectPadding = 4
	}
}

// Validate rejects handler declarations that cannot be delivered safely.
func (c *Config) Validate() error {
	for name, handler := range c.Handlers {
		if !handlerNamePattern.MatchString(name) {
			return fmt.Errorf("handler %q: name must contain only lowercase letters, numbers, hyphens, and underscores", name)
		}
		if strings.TrimSpace(handler.Run) == "" {
			return fmt.Errorf("handler %q: run is required", name)
		}
		if len(handler.On) == 0 {
			return fmt.Errorf("handler %q: on must contain at least one event type (or \"*\")", name)
		}
		for _, eventType := range handler.On {
			if strings.TrimSpace(eventType) == "" {
				return fmt.Errorf("handler %q: on contains an empty event type", name)
			}
		}
	}
	return nil
}

// HandlerNames returns configured handler names in deterministic order.
// Handlers must not rely on this order for correctness; each owns its cursor.
func (c *Config) HandlerNames() []string {
	names := make([]string, 0, len(c.Handlers))
	for name := range c.Handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasStatus reports whether s is a configured status.
func (c *Config) HasStatus(s string) bool {
	for _, st := range c.Statuses {
		if st == s {
			return true
		}
	}
	return false
}

// IsTerminal reports whether s is a terminal (closed) status.
func (c *Config) IsTerminal(s string) bool {
	for _, t := range c.Terminal {
		if t == s {
			return true
		}
	}
	return false
}

// RelByName returns the configured relationship type, or false.
func (c *Config) RelByName(name string) (RelType, bool) {
	for _, r := range c.Relationships {
		if r.Name == name {
			return r, true
		}
		// Allow referring to a relationship by its inverse name too.
		if r.Inverse == name {
			return RelType{Name: r.Inverse, Inverse: r.Name}, true
		}
	}
	return RelType{}, false
}

// RelNames returns every relationship kind name, including inverses, for
// validation and frontmatter scaffolding.
func (c *Config) RelNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range c.Relationships {
		for _, n := range []string{r.Name, r.Inverse} {
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}
