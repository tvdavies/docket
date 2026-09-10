package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/store"
	"gopkg.in/yaml.v3"
)

// EnablePlugin mutates a declared config in memory. When adoptLegacy is true,
// matching workspace handlers and explicitly pinned contributed statuses are
// removed in the same eventual config write.
func EnablePlugin(config *Config, manifest *plugin.Manifest, use PluginUse, adoptLegacy bool) error {
	if config.Plugins.Values == nil {
		config.Plugins.Values = map[string]PluginUse{}
	}
	if _, exists := config.Plugins.Values[manifest.Name]; !exists {
		config.Plugins.Order = append(config.Plugins.Order, manifest.Name)
	}
	config.Plugins.Values[manifest.Name] = use
	if adoptLegacy {
		for name := range manifest.Handlers {
			delete(config.Handlers, name)
		}
		for _, status := range manifest.Statuses {
			config.Statuses = slices.DeleteFunc(config.Statuses, func(value string) bool { return value == status.Name })
			config.Terminal = slices.DeleteFunc(config.Terminal, func(value string) bool { return value == status.Name })
		}
	}
	return config.Validate()
}

func DisablePlugin(config *Config, name string) bool {
	if _, exists := config.Plugins.Values[name]; !exists {
		return false
	}
	delete(config.Plugins.Values, name)
	config.Plugins.Order = slices.DeleteFunc(config.Plugins.Order, func(value string) bool { return value == name })
	return true
}

// ValidatePluginCandidate checks every contribution and config value using a
// staged manifest without changing the active machine registry.
func ValidatePluginCandidate(config *Config, manifest *plugin.Manifest, instanceConfig map[string]any) error {
	_, _, err := ComposeWithCandidate(config, manifest, instanceConfig)
	return err
}

// ComposeWithCandidate composes the effective config exactly as OpenRoot
// would, except that the named manifest overrides its registry entry. Handoff
// validation uses it to compare effective before/after configs under lock.
func ComposeWithCandidate(config *Config, manifest *plugin.Manifest, instanceConfig map[string]any) (*Config, []LoadedPlugin, error) {
	return composePluginsWithOverrides(config, map[string]plugin.Installed{
		manifest.Name: {Name: manifest.Name, Path: manifest.Root, Version: manifest.Version, Config: instanceConfig},
	})
}

// MutateDeclaredConfig serialises a complete read/validate/write transaction on
// config.yaml. The callback may also prepare inert cursor state before the
// atomic config publication.
func MutateDeclaredConfig(root string, mutate func(*Config) error) error {
	path := filepath.Join(root, "config.yaml")
	return store.WithLock(path+".lock", func() error {
		config, err := loadConfig(path)
		if err != nil {
			return err
		}
		if err := mutate(config); err != nil {
			return err
		}
		return writeDeclaredConfig(path, config)
	})
}

// DeclaredConfigLockPath is the lock serialising every declared config.yaml
// read/modify/write transaction for the workspace rooted at root.
func DeclaredConfigLockPath(root string) string {
	return filepath.Join(root, "config.yaml.lock")
}

// ConfigTransaction exposes the exact declared config bytes captured under the
// config lock so a caller can validate, prepare inert state and then publish
// one exact replacement in the same critical section.
type ConfigTransaction struct {
	Path string
	// Bytes are the exact current file contents; Hash is their SHA-256 hex.
	Bytes []byte
	Hash  string
	// Config is the parsed, validated declared config.
	Config    *Config
	published bool
}

// Publication describes one config publication outcome.
type Publication struct {
	Hash   string            `json:"hash"`
	Report store.WriteReport `json:"report"`
}

// ConfigHash hashes exact declared config bytes.
func ConfigHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SerializeDeclaredConfig validates composition and renders the exact bytes
// that a publication would write, without touching the filesystem.
func SerializeDeclaredConfig(config *Config) ([]byte, error) {
	if _, _, err := composePlugins(config); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	header := []byte("# docket workspace config. Statuses double as board lanes, in order.\n")
	return append(header, data...), nil
}

// WithDeclaredConfigTransaction holds the workspace config lock while fn runs
// with the exact current bytes. fn may call tx.Publish at most once to replace
// config.yaml atomically; returning without publishing leaves the file
// untouched. Callers that also need handler locks must acquire them before
// entering, matching the handler-before-config order used by plugin enable.
func WithDeclaredConfigTransaction(ctx context.Context, root string, fn func(tx *ConfigTransaction) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	path := filepath.Join(root, "config.yaml")
	return store.WithLockContext(ctx, DeclaredConfigLockPath(root), func() error {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		cfg, err := ParseDeclaredConfig(data)
		if err != nil {
			return err
		}
		return fn(&ConfigTransaction{Path: path, Bytes: data, Hash: ConfigHash(data), Config: cfg})
	})
}

// Publish atomically replaces config.yaml with exact, already validated bytes
// and reports the rename/directory-sync outcome. It is the single ownership
// publication point for a handler handoff: readers see either the old or the
// new complete file.
func (tx *ConfigTransaction) Publish(data []byte, observers ...func(string)) (Publication, error) {
	if tx.published {
		return Publication{}, fmt.Errorf("config transaction has already published")
	}
	tx.published = true
	report, err := store.WriteAtomicReport(tx.Path, data, 0o644, observers...)
	return Publication{Hash: ConfigHash(data), Report: report}, err
}

// WithDeclaredConfigLocks holds every named workspace config lock in stable
// path order while fn runs. Registry-wide plugin mutations use this to keep the
// workspace snapshots they validate unchanged through the registry flip.
func WithDeclaredConfigLocks(paths []string, fn func() error) error {
	locks := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		root, err := declaredRoot(path)
		if err != nil {
			return err
		}
		lock := filepath.Join(root, "config.yaml.lock")
		if !seen[lock] {
			seen[lock] = true
			locks = append(locks, lock)
		}
	}
	sort.Strings(locks)
	var acquire func(int) error
	acquire = func(index int) error {
		if index == len(locks) {
			return fn()
		}
		return store.WithLock(locks[index], func() error { return acquire(index + 1) })
	}
	return acquire(0)
}

// DeclaresPluginRoot reads only the plugins mapping. It deliberately skips the
// rest of workspace validation so an unrelated semantically invalid workspace
// does not block administration of a plugin it does not enable.
func DeclaresPluginRoot(path, name string) (bool, error) {
	root, err := declaredRoot(path)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		return false, err
	}
	var probe struct {
		Plugins PluginUses `yaml:"plugins,omitempty"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return false, fmt.Errorf("parse config: %w", err)
	}
	_, enabled := probe.Plugins.Values[name]
	return enabled, nil
}

// WriteDeclaredConfig validates composition and atomically replaces config.yaml.
// Prefer MutateDeclaredConfig for user-facing read/modify/write operations.
func WriteDeclaredConfig(root string, config *Config) error {
	path := filepath.Join(root, "config.yaml")
	return store.WithLock(path+".lock", func() error { return writeDeclaredConfig(path, config) })
}

func writeDeclaredConfig(path string, config *Config) error {
	data, err := SerializeDeclaredConfig(config)
	if err != nil {
		return err
	}
	return store.WriteAtomic(path, data, 0o644)
}

// CloneDeclared returns an independently mutable copy of a workspace's source
// config rather than its composed runtime config.
func CloneDeclared(ws *Workspace) (*Config, error) {
	if ws.DeclaredConfig == nil {
		return nil, fmt.Errorf("workspace has no declared config")
	}
	data, err := yaml.Marshal(ws.DeclaredConfig)
	if err != nil {
		return nil, err
	}
	var result Config
	if err := yaml.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	result.applyDefaults()
	return &result, nil
}
