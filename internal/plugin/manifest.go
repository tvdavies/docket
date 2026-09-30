// Package plugin defines Docket's trusted local plugin manifest and validates
// its declarative extension points. Installation and workspace composition are
// kept in higher-level packages so this package remains dependency-light.
//
// Docket runs a plugin's hooks, statuses, configuration and CLI. The service
// and ui sections, and config options_from, are legacy presentation metadata:
// they are still parsed and validated so existing manifests keep loading, but
// Docket no longer serves UI, proxies services, fetches options or launches
// service.command.
package plugin

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const ManifestFile = "docket-plugin.yaml"

// EngineVersion is set by the CLI before opening workspaces. Direct library
// callers and development builds intentionally use dev, which satisfies floors.
var EngineVersion = "dev"

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Manifest is the complete v1 plugin declaration.
type Manifest struct {
	Name        string             `yaml:"name" json:"name"`
	Version     string             `yaml:"version" json:"version"`
	Description string             `yaml:"description,omitempty" json:"description,omitempty"`
	Requires    Requirements       `yaml:"requires,omitempty" json:"requires,omitempty"`
	Handlers    map[string]Handler `yaml:"handlers,omitempty" json:"handlers,omitempty"`
	Statuses    []Status           `yaml:"statuses,omitempty" json:"statuses,omitempty"`
	Config      ConfigSchemas      `yaml:"config,omitempty" json:"config,omitempty"`
	CLI         *CLI               `yaml:"cli,omitempty" json:"cli,omitempty"`
	// Service and UI are legacy metadata; see the package documentation.
	Service *Service `yaml:"service,omitempty" json:"service,omitempty"`
	UI      UI       `yaml:"ui,omitempty" json:"ui,omitempty"`
	Root    string   `yaml:"-" json:"root"`
}

type Requirements struct {
	Docket string `yaml:"docket,omitempty" json:"docket,omitempty"`
}

type Handler struct {
	On       []string       `yaml:"on" json:"on"`
	Match    map[string]any `yaml:"match,omitempty" json:"match,omitempty"`
	Run      string         `yaml:"run,omitempty" json:"run,omitempty"`
	Lua      string         `yaml:"lua,omitempty" json:"lua,omitempty"`
	Delivery string         `yaml:"delivery,omitempty" json:"delivery,omitempty"`
}

type Status struct {
	Name     string `yaml:"name" json:"name"`
	After    string `yaml:"after" json:"after"`
	Terminal bool   `yaml:"terminal,omitempty" json:"terminal,omitempty"`
}

type ConfigSchemas struct {
	Instance  map[string]ConfigField `yaml:"instance,omitempty" json:"instance,omitempty"`
	Workspace map[string]ConfigField `yaml:"workspace,omitempty" json:"workspace,omitempty"`
	Status    map[string]ConfigField `yaml:"status,omitempty" json:"status,omitempty"`
}

type ConfigField struct {
	Type        string `yaml:"type" json:"type"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Default     any    `yaml:"default,omitempty" json:"default,omitempty"`
	Enum        []any  `yaml:"enum,omitempty" json:"enum,omitempty"`
	Secret      bool   `yaml:"secret,omitempty" json:"secret,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// OptionsFrom is legacy metadata naming a service path that listed the
	// field's choices for the retired settings UI. It is parsed, not fetched.
	OptionsFrom string `yaml:"options_from,omitempty" json:"options_from,omitempty"`
}

type Service struct {
	URL     string `yaml:"url" json:"url"`
	Healthz string `yaml:"healthz,omitempty" json:"healthz,omitempty"`
	Auth    string `yaml:"auth,omitempty" json:"auth,omitempty"`
	// Command was run and supervised by earlier Docket services. It is still
	// validated, but Docket no longer launches it; see HostingProblem.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	// Watch lists plugin-relative globs that restarted Command.
	Watch []string `yaml:"watch,omitempty" json:"watch,omitempty"`
}

// HostingProblem describes a legacy service.command that Docket no longer
// launches, or "" when the plugin needs no hosting. The runner reports it so
// an operator can move the process to an OS or container supervisor. It never
// prevents the plugin's hooks, CLI or configuration from loading.
func (m *Manifest) HostingProblem() string {
	if m.Service == nil || len(m.Service.Command) == 0 {
		return ""
	}
	return fmt.Sprintf("service.command %q is no longer launched by Docket; run it under systemd, a container or another supervisor", strings.Join(m.Service.Command, " "))
}

type CLI struct {
	Run string `yaml:"run" json:"run"`
}

// UI is legacy browser metadata from the retired board. It is parsed and
// validated for compatibility; Docket serves none of it. Historical widget records are
// read directly from the event log, independently of these declarations.
type UI struct {
	// Dir held static UI assets. Its contents are no longer read.
	Dir                string              `yaml:"dir,omitempty" json:"dir,omitempty"`
	Capabilities       []string            `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Widgets            []Widget            `yaml:"widgets,omitempty" json:"widgets,omitempty"`
	Panels             []View              `yaml:"panels,omitempty" json:"panels,omitempty"`
	Pages              []View              `yaml:"pages,omitempty" json:"pages,omitempty"`
	ReferenceResolvers []ReferenceResolver `yaml:"reference_resolvers,omitempty" json:"reference_resolvers,omitempty"`

	// Deprecated: api_version and cards describe the retired build-time UI.
	// Cards with api_version >= 2 still declare presentation-only widgets.
	APIVersion int    `yaml:"api_version,omitempty" json:"api_version,omitempty"`
	Cards      []Card `yaml:"cards,omitempty" json:"cards,omitempty"`
}

// Widget describes a legacy task-ledger presentation. Docket only validates
// this metadata; it no longer opens the iframe or applies presentation slots.
type Widget struct {
	Type  string   `yaml:"type" json:"type"`
	Title string   `yaml:"title" json:"title"`
	Entry string   `yaml:"entry,omitempty" json:"entry,omitempty"`
	Slots []string `yaml:"slots,omitempty" json:"slots"`
}

// View describes a legacy iframe contribution; Docket no longer serves it.
type View struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	Entry string `yaml:"entry" json:"entry"`
}

type Card struct {
	Locations []string `yaml:"locations,omitempty" json:"locations,omitempty"`
	Type      string   `yaml:"type" json:"type"`
	Title     string   `yaml:"title" json:"title"`
}

type ReferenceResolver struct {
	ID      string   `yaml:"id" json:"id"`
	Pattern string   `yaml:"pattern" json:"pattern"`
	Kinds   []string `yaml:"kinds,omitempty" json:"kinds,omitempty"`
	// Endpoint is a service path that receives the reference as JSON and
	// returns {label, icon?, meta?, href?}. Without it references show as-is.
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
}

// UI capabilities a sandboxed frame may request through the host bridge.
var Capabilities = []string{"task.read", "task.comment", "task.move", "service.fetch", "service.stream", "open.external"}

// WidgetSlots are the placements a widget presentation may use.
var WidgetSlots = []string{"board", "activity"}

// EffectiveConfig is the validated configuration delivered to a plugin.
type EffectiveConfig struct {
	Values   map[string]any            `json:"config"`
	Statuses map[string]map[string]any `json:"status_config"`
}

// Load parses and strictly validates a manifest in root.
func Load(root, engineVersion string) (*Manifest, error) {
	if engineVersion == "" {
		engineVersion = EngineVersion
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(absolute, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("read plugin manifest: %w", err)
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse plugin manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse plugin manifest: multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("parse plugin manifest: %w", err)
	}
	manifest.Root = absolute
	if err := manifest.Validate(engineVersion); err != nil {
		return nil, fmt.Errorf("validate plugin manifest %s: %w", filepath.Join(absolute, ManifestFile), err)
	}
	return &manifest, nil
}

func (m *Manifest) Validate(engineVersion string) error {
	if !namePattern.MatchString(m.Name) {
		return fmt.Errorf("name %q must contain only lowercase letters, numbers, hyphens, and underscores", m.Name)
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("version is required")
	}
	if _, err := parseVersion(m.Version); err != nil {
		return fmt.Errorf("version: %w", err)
	}
	if m.Requires.Docket != "" {
		floor, ok := strings.CutPrefix(strings.TrimSpace(m.Requires.Docket), ">=")
		if !ok || strings.TrimSpace(floor) == "" {
			return errors.New("requires.docket must be a >= semver floor")
		}
		if engineVersion != "" && engineVersion != "dev" {
			want, err := parseVersion(strings.TrimSpace(floor))
			if err != nil {
				return fmt.Errorf("requires.docket: %w", err)
			}
			have, err := parseVersion(engineVersion)
			if err != nil {
				return fmt.Errorf("engine version %q is not semver", engineVersion)
			}
			if compareVersion(have, want) < 0 {
				return fmt.Errorf("requires docket %s, current version is %s", m.Requires.Docket, engineVersion)
			}
		}
	}
	for name, handler := range m.Handlers {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("handler %q: invalid name", name)
		}
		if err := validateHandler(name, handler); err != nil {
			return err
		}
		path := handler.Run
		if path == "" {
			path = handler.Lua
		}
		if err := validateRelativePath("handler "+name, path); err != nil {
			return err
		}
	}
	statusNames := map[string]bool{}
	for _, status := range m.Statuses {
		if !namePattern.MatchString(status.Name) {
			return fmt.Errorf("status %q: invalid name", status.Name)
		}
		if !namePattern.MatchString(status.After) {
			return fmt.Errorf("status %q: after must name a route-safe status", status.Name)
		}
		if statusNames[status.Name] {
			return fmt.Errorf("status %q is duplicated", status.Name)
		}
		statusNames[status.Name] = true
	}
	for scope, schema := range map[string]map[string]ConfigField{
		"instance": m.Config.Instance, "workspace": m.Config.Workspace, "status": m.Config.Status,
	} {
		if err := validateSchema(scope, schema); err != nil {
			return err
		}
		for name, field := range schema {
			if field.OptionsFrom != "" && m.Service == nil {
				return fmt.Errorf("config.%s.%s: options_from requires a service", scope, name)
			}
		}
	}
	if m.Service != nil {
		if err := validateService(*m.Service); err != nil {
			return err
		}
	}
	if m.CLI != nil {
		if err := validateRelativePath("cli.run", m.CLI.Run); err != nil {
			return err
		}
	}
	if m.UI.APIVersion < 0 {
		return errors.New("ui.api_version must be positive")
	}
	seenCards := map[string]bool{}
	for _, card := range m.UI.Cards {
		if card.Type == "" || !strings.HasPrefix(card.Type, m.Name+"/") {
			return fmt.Errorf("ui card type %q must be namespaced %s/...", card.Type, m.Name)
		}
		if seenCards[card.Type] {
			return fmt.Errorf("ui card type %q is duplicated", card.Type)
		}
		seenCards[card.Type] = true
		if m.UI.APIVersion >= 2 && len(card.Locations) == 0 {
			return fmt.Errorf("ui card %q requires locations", card.Type)
		}
		seenLocations := map[string]bool{}
		for _, location := range card.Locations {
			if (location != "board" && location != "activity") || seenLocations[location] {
				return fmt.Errorf("ui card %q has invalid/duplicate location %q", card.Type, location)
			}
			seenLocations[location] = true
		}
	}
	seenResolvers := map[string]bool{}
	for _, resolver := range m.UI.ReferenceResolvers {
		if resolver.ID == "" || !strings.HasPrefix(resolver.ID, m.Name+"/") {
			return fmt.Errorf("reference resolver id %q must be namespaced %s/...", resolver.ID, m.Name)
		}
		if seenResolvers[resolver.ID] {
			return fmt.Errorf("reference resolver %q is duplicated", resolver.ID)
		}
		if _, err := regexp.Compile(resolver.Pattern); err != nil {
			return fmt.Errorf("reference resolver %q pattern: %w", resolver.ID, err)
		}
		if resolver.Endpoint != "" {
			if m.Service == nil {
				return fmt.Errorf("reference resolver %q endpoint requires a service", resolver.ID)
			}
			if err := validateServicePath("reference resolver "+resolver.ID+" endpoint", resolver.Endpoint); err != nil {
				return err
			}
		}
		seenResolvers[resolver.ID] = true
	}
	return m.validateFrames(seenCards)
}

func (m *Manifest) validateFrames(seenTypes map[string]bool) error {
	if m.UI.Dir != "" {
		if err := validateRelativePath("ui.dir", m.UI.Dir); err != nil {
			return err
		}
	}
	seenCapabilities := map[string]bool{}
	for _, capability := range m.UI.Capabilities {
		if !slices.Contains(Capabilities, capability) || seenCapabilities[capability] {
			return fmt.Errorf("ui capability %q is unknown or duplicated (allowed: %s)", capability, strings.Join(Capabilities, ", "))
		}
		if strings.HasPrefix(capability, "service.") && m.Service == nil {
			return fmt.Errorf("ui capability %q requires a service", capability)
		}
		seenCapabilities[capability] = true
	}
	entry := func(field, value string, required bool) error {
		if value == "" && !required {
			return nil
		}
		if m.UI.Dir == "" {
			return fmt.Errorf("%s requires ui.dir", field)
		}
		return validateAssetPath(field, value)
	}
	for _, declared := range m.UI.Widgets {
		if declared.Type == "" || !strings.HasPrefix(declared.Type, m.Name+"/") || !widgetTypePattern.MatchString(declared.Type) {
			return fmt.Errorf("ui widget type %q must be namespaced %s/...", declared.Type, m.Name)
		}
		if seenTypes[declared.Type] {
			return fmt.Errorf("ui widget type %q is duplicated", declared.Type)
		}
		seenTypes[declared.Type] = true
		if strings.TrimSpace(declared.Title) == "" {
			return fmt.Errorf("ui widget %q requires a title", declared.Type)
		}
		seenSlots := map[string]bool{}
		for _, slot := range declared.Slots {
			if !slices.Contains(WidgetSlots, slot) || seenSlots[slot] {
				return fmt.Errorf("ui widget %q has invalid/duplicate slot %q (allowed: %s)", declared.Type, slot, strings.Join(WidgetSlots, ", "))
			}
			seenSlots[slot] = true
		}
		if err := entry("ui widget "+declared.Type+" entry", declared.Entry, false); err != nil {
			return err
		}
	}
	for kind, views := range map[string][]View{"panel": m.UI.Panels, "page": m.UI.Pages} {
		seen := map[string]bool{}
		for _, view := range views {
			if !namePattern.MatchString(view.ID) || seen[view.ID] {
				return fmt.Errorf("ui %s id %q must be unique and match %s", kind, view.ID, namePattern)
			}
			seen[view.ID] = true
			if strings.TrimSpace(view.Title) == "" {
				return fmt.Errorf("ui %s %q requires a title", kind, view.ID)
			}
			if err := entry("ui "+kind+" "+view.ID+" entry", view.Entry, true); err != nil {
				return err
			}
		}
	}
	return nil
}

var widgetTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*/[a-zA-Z0-9][a-zA-Z0-9_/-]{0,98}$`)

// validateAssetPath accepts a clean, relative URL path such as "widget.html"
// or "pages/fleet.html". Query strings and fragments are not part of an entry.
func validateAssetPath(field, value string) error {
	if value == "" || strings.ContainsAny(value, "\\?#%") || strings.HasPrefix(value, "/") || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("%s must be a clean relative path inside ui.dir", field)
	}
	return nil
}

// validateServicePath accepts an absolute service path without traversal.
func validateServicePath(field, value string) error {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\?#%") || path.Clean(value) != value {
		return fmt.Errorf("%s must be a clean absolute service path", field)
	}
	return nil
}

func validateHandler(name string, handler Handler) error {
	hasRun := strings.TrimSpace(handler.Run) != ""
	hasLua := strings.TrimSpace(handler.Lua) != ""
	if hasRun == hasLua {
		return fmt.Errorf("handler %q: exactly one of run or lua is required", name)
	}
	if len(handler.On) == 0 {
		return fmt.Errorf("handler %q: on must contain at least one event type", name)
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
			return fmt.Errorf("handler %q: only data fields may use nested match paths", name)
		}
	}
	if handler.Delivery != "" && handler.Delivery != "inline" && handler.Delivery != "service" {
		return fmt.Errorf("handler %q: delivery must be inline or service", name)
	}
	return nil
}

func validateSchema(scope string, schema map[string]ConfigField) error {
	for name, field := range schema {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("config.%s key %q: invalid name", scope, name)
		}
		switch field.Type {
		case "string", "number", "boolean", "list", "map":
		default:
			return fmt.Errorf("config.%s.%s: type must be string, number, boolean, list, or map", scope, name)
		}
		if field.Secret && scope != "instance" {
			return fmt.Errorf("config.%s.%s: secret fields are only valid at instance scope", scope, name)
		}
		if field.Secret && (field.Default != nil || len(field.Enum) > 0) {
			return fmt.Errorf("config.%s.%s: secret fields cannot declare defaults or enums", scope, name)
		}
		if field.Default != nil {
			if err := validateFieldValue(field, field.Default); err != nil {
				return fmt.Errorf("config.%s.%s default: %w", scope, name, err)
			}
		}
		for _, candidate := range field.Enum {
			if err := validateFieldValue(field, candidate); err != nil {
				return fmt.Errorf("config.%s.%s enum: %w", scope, name, err)
			}
		}
		if field.OptionsFrom != "" {
			if field.Type != "string" && field.Type != "number" {
				return fmt.Errorf("config.%s.%s: options_from is only valid on string or number fields", scope, name)
			}
			if field.Secret || len(field.Enum) > 0 {
				return fmt.Errorf("config.%s.%s: options_from cannot be combined with secret or enum", scope, name)
			}
			if err := validateServicePath(fmt.Sprintf("config.%s.%s options_from", scope, name), field.OptionsFrom); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateService(service Service) error {
	target, err := url.Parse(service.URL)
	if err != nil || target.Scheme != "http" || target.User != nil || target.Host == "" {
		return errors.New("service.url must be a loopback http URL")
	}
	host := target.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("service.url must target loopback in v1")
		}
	}
	if service.Auth != "" && service.Auth != "none" {
		return errors.New("service.auth must be absent or none in v1")
	}
	if service.Healthz != "" && !strings.HasPrefix(service.Healthz, "/") {
		return errors.New("service.healthz must start with /")
	}
	for index, argument := range service.Command {
		if strings.TrimSpace(argument) == "" {
			return fmt.Errorf("service.command[%d] is empty", index)
		}
	}
	if len(service.Command) > 0 && strings.Contains(service.Command[0], "/") {
		if err := validateRelativePath("service.command[0]", service.Command[0]); err != nil {
			return err
		}
	}
	if len(service.Watch) > 0 && len(service.Command) == 0 {
		return errors.New("service.watch requires service.command")
	}
	for _, pattern := range service.Watch {
		if err := validateWatchPattern(pattern); err != nil {
			return err
		}
	}
	return nil
}

func validateWatchPattern(pattern string) error {
	if pattern == "" || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") || path.Clean(pattern) != pattern || pattern == ".." || strings.HasPrefix(pattern, "../") {
		return fmt.Errorf("service.watch %q must be a clean plugin-relative glob", pattern)
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("service.watch %q: %w", pattern, err)
		}
	}
	return nil
}

func validateRelativePath(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if filepath.IsAbs(value) || value == ".." || strings.HasPrefix(filepath.Clean(value), ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay inside the plugin directory", field)
	}
	return nil
}

// ResolveInstanceConfig validates instance-scoped values and applies defaults.
func (m *Manifest) ResolveInstanceConfig(values map[string]any) (map[string]any, error) {
	return resolveScope("instance", m.Config.Instance, values)
}

// ResolveConfig applies defaults and validates instance, workspace, and
// status-scoped values. Workspace values override instance values.
func (m *Manifest) ResolveConfig(instance, workspaceValues map[string]any, statusValues map[string]map[string]any, statuses []string) (EffectiveConfig, error) {
	instanceResolved, err := resolveScope("instance", m.Config.Instance, instance)
	if err != nil {
		return EffectiveConfig{}, err
	}
	workspaceResolved, err := resolveScope("workspace", m.Config.Workspace, workspaceValues)
	if err != nil {
		return EffectiveConfig{}, err
	}
	values := instanceResolved
	if values == nil {
		values = map[string]any{}
	}
	for key, value := range workspaceResolved {
		values[key] = value
	}
	allowedStatuses := map[string]bool{}
	for _, status := range statuses {
		allowedStatuses[status] = true
	}
	for status := range statusValues {
		if !allowedStatuses[status] {
			return EffectiveConfig{}, fmt.Errorf("config.status.%s: unknown composed status", status)
		}
	}
	resolvedStatuses := map[string]map[string]any{}
	for _, status := range statuses {
		resolved, err := resolveScope("status."+status, m.Config.Status, statusValues[status])
		if err != nil {
			return EffectiveConfig{}, err
		}
		if len(resolved) > 0 {
			resolvedStatuses[status] = resolved
		}
	}
	return EffectiveConfig{Values: values, Statuses: resolvedStatuses}, nil
}

func resolveScope(scope string, schema map[string]ConfigField, input map[string]any) (map[string]any, error) {
	if input == nil {
		input = map[string]any{}
	}
	for key := range input {
		if _, ok := schema[key]; !ok {
			return nil, fmt.Errorf("config.%s.%s is not declared by the plugin", scope, key)
		}
	}
	result := map[string]any{}
	for key, field := range schema {
		value, exists := input[key]
		if field.Secret && exists {
			return nil, fmt.Errorf("config.%s.%s is secret and must be supplied through the hook environment", scope, key)
		}
		if !exists && field.Default != nil {
			value, exists = cloneValue(field.Default), true
		}
		if !exists {
			if field.Required && !field.Secret {
				return nil, fmt.Errorf("config.%s.%s is required", scope, key)
			}
			continue
		}
		if err := validateFieldValue(field, value); err != nil {
			return nil, fmt.Errorf("config.%s.%s: %w", scope, key, err)
		}
		result[key] = cloneValue(value)
	}
	return result, nil
}

func validateFieldValue(field ConfigField, value any) error {
	valid := false
	switch field.Type {
	case "string":
		_, valid = value.(string)
	case "number":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			valid = true
		}
	case "boolean":
		_, valid = value.(bool)
	case "list":
		switch value.(type) {
		case []any, []string:
			valid = true
		}
	case "map":
		switch value.(type) {
		case map[string]any, map[any]any:
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("must be %s", field.Type)
	}
	if len(field.Enum) > 0 {
		encoded, _ := yaml.Marshal(value)
		matched := false
		for _, candidate := range field.Enum {
			other, _ := yaml.Marshal(candidate)
			if string(encoded) == string(other) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("must be one of %v", field.Enum)
		}
	}
	return nil
}

func cloneValue(value any) any {
	data, _ := yaml.Marshal(value)
	var result any
	_ = yaml.Unmarshal(data, &result)
	return result
}

type version [3]int

func parseVersion(value string) (version, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	value = strings.SplitN(value, "-", 2)[0]
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("%q must be semantic version major.minor.patch", value)
	}
	var result version
	for index, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version{}, fmt.Errorf("%q must be semantic version major.minor.patch", value)
		}
		result[index] = n
	}
	return result, nil
}

func compareVersion(left, right version) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}
