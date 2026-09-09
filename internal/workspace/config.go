package workspace

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	handlerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	idPrefixPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

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
	// cursor. Executables receive JSON lines; Lua handlers receive event tables.
	Handlers map[string]HandlerConfig `yaml:"handlers,omitempty"`
	// Plugins declares instance-installed plugins enabled for this workspace.
	// Declaration order is retained because contribution precedence is ordered.
	Plugins  PluginUses `yaml:"plugins,omitempty"`
	Settings Settings   `yaml:"settings"`
}

// PluginUse contains the workspace- and status-scoped values for one plugin.
type PluginUse struct {
	Config   map[string]any            `yaml:"config,omitempty" json:"config,omitempty"`
	Statuses map[string]map[string]any `yaml:"statuses,omitempty" json:"statuses,omitempty"`
}

// PluginUses is a YAML mapping with stable declaration order.
type PluginUses struct {
	Order  []string             `yaml:"-" json:"order"`
	Values map[string]PluginUse `yaml:"-" json:"values"`
}

func (p *PluginUses) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("plugins must be a mapping")
	}
	p.Values = map[string]PluginUse{}
	for index := 0; index < len(node.Content); index += 2 {
		name := node.Content[index].Value
		if _, exists := p.Values[name]; exists {
			return fmt.Errorf("plugin %q is duplicated", name)
		}
		var value PluginUse
		if err := node.Content[index+1].Decode(&value); err != nil {
			return fmt.Errorf("plugin %q: %w", name, err)
		}
		p.Order = append(p.Order, name)
		p.Values[name] = value
	}
	return nil
}

func (p PluginUses) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range p.Order {
		value, ok := p.Values[name]
		if !ok {
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
		valueNode := &yaml.Node{}
		if err := valueNode.Encode(value); err != nil {
			return nil, err
		}
		node.Content = append(node.Content, keyNode, valueNode)
	}
	return node, nil
}

func (p PluginUses) IsZero() bool { return len(p.Order) == 0 }

// HandlerConfig registers one event consumer. Exactly one of Run or Lua is
// required. Relative paths resolve from the project root (the parent of
// .docket/). Lua handlers execute in an isolated child Docket process.
type HandlerConfig struct {
	On    []string       `yaml:"on"`
	Match map[string]any `yaml:"match,omitempty"`
	Run   string         `yaml:"run,omitempty"`
	Lua   string         `yaml:"lua,omitempty"`
	// Delivery is "inline" (the default) or "service". Service delivery leaves
	// the handler cursor pending for docket.service so the mutating CLI returns
	// immediately while retaining durable, retryable execution.
	Delivery string `yaml:"delivery,omitempty"`
	// Runtime-only plugin context. Workspace YAML never serialises these fields.
	PluginName         string                    `yaml:"-" json:"-"`
	PluginRoot         string                    `yaml:"-" json:"-"`
	PluginConfig       map[string]any            `yaml:"-" json:"-"`
	PluginStatusConfig map[string]map[string]any `yaml:"-" json:"-"`
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

// Validate rejects configuration that cannot be stored or delivered safely.
func (c *Config) Validate() error {
	if !idPrefixPattern.MatchString(c.Settings.IDPrefix) {
		return fmt.Errorf("settings.id_prefix must contain only letters, numbers, hyphens, and underscores")
	}
	if !idPrefixPattern.MatchString(c.Settings.ProjectPrefix) {
		return fmt.Errorf("settings.project_prefix must contain only letters, numbers, hyphens, and underscores")
	}
	if c.Settings.IDPadding < 1 || c.Settings.ProjectPadding < 1 {
		return fmt.Errorf("id padding values must be positive")
	}
	for _, name := range c.Plugins.Order {
		if !handlerNamePattern.MatchString(name) {
			return fmt.Errorf("plugin %q: name must contain only lowercase letters, numbers, hyphens, and underscores", name)
		}
		if _, ok := c.Plugins.Values[name]; !ok {
			return fmt.Errorf("plugin %q: declaration is missing", name)
		}
	}
	for name, handler := range c.Handlers {
		validName := handlerNamePattern.MatchString(name)
		if handler.PluginName != "" {
			validName = strings.HasPrefix(name, handler.PluginName+"/") && handlerNamePattern.MatchString(strings.TrimPrefix(name, handler.PluginName+"/"))
		}
		if !validName {
			return fmt.Errorf("handler %q: name must contain only lowercase letters, numbers, hyphens, and underscores", name)
		}
		hasRun := strings.TrimSpace(handler.Run) != ""
		hasLua := strings.TrimSpace(handler.Lua) != ""
		if hasRun == hasLua {
			return fmt.Errorf("handler %q: exactly one of run or lua is required", name)
		}
		if len(handler.On) == 0 {
			return fmt.Errorf("handler %q: on must contain at least one event type (or \"*\")", name)
		}
		for _, eventType := range handler.On {
			if strings.TrimSpace(eventType) == "" {
				return fmt.Errorf("handler %q: on contains an empty event type", name)
			}
		}
		for path := range handler.Match {
			parts := strings.Split(path, ".")
			for _, part := range parts {
				if strings.TrimSpace(part) == "" {
					return fmt.Errorf("handler %q: match path %q is invalid", name, path)
				}
			}
			switch parts[0] {
			case "seq", "time", "type", "task", "title", "actor", "assignee", "data":
			default:
				return fmt.Errorf("handler %q: match path %q has an unknown event field", name, path)
			}
			if len(parts) > 1 && parts[0] != "data" {
				return fmt.Errorf("handler %q: only data fields may use nested match paths (got %q)", name, path)
			}
		}
		if handler.Delivery != "" && handler.Delivery != "inline" && handler.Delivery != "service" {
			return fmt.Errorf("handler %q: delivery must be \"inline\" or \"service\"", name)
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
