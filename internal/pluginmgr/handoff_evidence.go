package pluginmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

var evidenceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var evidenceHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Reject aliases before opening lock or cursor paths. This is a cooperative
// filesystem protocol, not a defence against a hostile writer racing lstat.
func plainPath(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink path is not supported: %s", current)
			}
			if !info.IsDir() {
				if current != path || !info.Mode().IsRegular() {
					return fmt.Errorf("not a regular file/directory: %s", current)
				}
				if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
					return fmt.Errorf("hard-linked file is not supported: %s", current)
				}
			}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func validateStatePaths(ws *workspace.Workspace, mapping map[string]string) error {
	for _, path := range []string{ws.Root, ws.Path("config.yaml"), workspace.DeclaredConfigLockPath(ws.Root), ws.EventsFile(), ws.HandlerStateDir()} {
		if err := plainPath(path); err != nil {
			return err
		}
	}
	for pluginID, legacy := range mapping {
		parts := strings.Split(pluginID, "/")
		if len(parts) != 2 || !evidenceName.MatchString(parts[0]) || !evidenceName.MatchString(legacy) || parts[1] != legacy {
			return fmt.Errorf("invalid one-to-one identity mapping %q -> %q", pluginID, legacy)
		}
		for _, name := range []string{pluginID, legacy} {
			for _, suffix := range []string{".cursor", ".lock"} {
				if err := plainPath(filepath.Join(ws.HandlerStateDir(), name+suffix)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Sync every directory introduced for receipt/cursor preparation before config
// publication. The caller has already rejected aliases and owns these paths.
func syncParents(path, through string) error {
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if err := store.SyncDir(dir); err != nil {
			return err
		}
		if dir == through {
			return nil
		}
		if filepath.Dir(dir) == dir {
			return fmt.Errorf("%s is not under %s", path, through)
		}
	}
}

func verifyCapturedFiles(tx *workspace.ConfigTransaction, receipt preparedReceipt) error {
	ws := &workspace.Workspace{Root: receipt.WorkspaceRoot}
	for _, source := range receipt.Sources {
		current, err := handlers.ReadCheckpoint(ws, source.Identity)
		if err != nil || current.Position != source.Position || current.PrefixHash != source.PrefixHash {
			return fmt.Errorf("source checkpoint drifted before publication: %s", source.Identity)
		}
		destination := receipt.Map[source.Identity]
		if receipt.Direction == Forward {
			destination = receipt.Plugin + "/" + source.Identity
		}
		prepared, err := handlers.ReadCheckpoint(ws, destination)
		if err != nil || prepared.Position != source.Position || prepared.PrefixHash != source.PrefixHash {
			return fmt.Errorf("destination checkpoint drifted before publication: %s", destination)
		}
	}
	hash, found, err := events.PrefixHash(ws, receipt.Ledger.ObservedEnd)
	if err != nil || found < receipt.Ledger.ObservedEnd || hash != receipt.Ledger.EndHash {
		return errors.New("ledger snapshot drifted before publication")
	}
	current, err := os.ReadFile(tx.Path)
	if err != nil {
		return err
	}
	if workspace.ConfigHash(current) != tx.Hash {
		return errors.New("declared config changed outside its lock")
	}
	registryBytes, err := os.ReadFile(receipt.RegistryPath)
	if err != nil || workspace.ConfigHash(registryBytes) != receipt.RegistrySHA256 {
		return errors.New("registry drifted outside its lock before publication")
	}
	manifest, err := os.ReadFile(filepath.Join(receipt.Provenance.PluginPath, plugin.ManifestFile))
	if err != nil {
		return err
	}
	if workspace.ConfigHash(manifest) != receipt.Provenance.ManifestSHA256 {
		return errors.New("plugin manifest drifted before publication")
	}
	for _, script := range receipt.Scripts {
		if err := plainPath(script.Path); err != nil {
			return err
		}
		current, err := os.ReadFile(script.Path)
		if err != nil || workspace.ConfigHash(current) != script.SHA256 {
			return fmt.Errorf("script drifted before publication: %s", script.Path)
		}
	}
	return nil
}

// validateReceipt validates the complete private evidence before inspection
// uses any identity as a path or any hash to classify ownership. No repair or
// lock files are created by this path.
func validateReceipt(ws *workspace.Workspace, dir string, r preparedReceipt) error {
	if r.Version != receiptVersion || r.AttemptID == "" || (r.Direction != Forward && r.Direction != Reverse) || !evidenceName.MatchString(r.Plugin) || len(r.Map) == 0 {
		return errors.New("invalid receipt schema, direction or map")
	}
	reg, err := registry.ConfigPath()
	if err != nil {
		return err
	}
	if !evidenceHash.MatchString(r.RegistrySHA256) || !evidenceHash.MatchString(r.Provenance.InstanceConfigSHA256) {
		return errors.New("missing registry/instance config hash evidence")
	}
	if r.RegistryPath != reg {
		return errors.New("receipt registry path differs from the current registry")
	}
	if err := plainPath(dir); err != nil {
		return err
	}
	if err := validateStatePaths(ws, r.Map); err != nil {
		return err
	}
	for identity := range r.Map {
		if !strings.HasPrefix(identity, r.Plugin+"/") {
			return errors.New("receipt map belongs to another plugin")
		}
	}
	for file, hash := range map[string]string{configBeforeFile: r.BeforeConfigHash, configTargetFile: r.TargetConfigHash} {
		if !evidenceHash.MatchString(hash) {
			return fmt.Errorf("invalid %s hash", file)
		}
		if err := plainPath(filepath.Join(dir, file)); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || workspace.ConfigHash(data) != hash {
			return fmt.Errorf("%s snapshot/hash mismatch", file)
		}
		cfg, err := workspace.ParseDeclaredConfig(data)
		if err != nil {
			return err
		}
		pluginActive := r.Direction == Reverse
		if file == configTargetFile {
			pluginActive = !pluginActive
		}
		_, enabled := cfg.Plugins.Values[r.Plugin]
		if enabled != pluginActive {
			return fmt.Errorf("%s does not select the recorded ownership", file)
		}
		for _, legacy := range r.Map {
			_, enabled := cfg.Handlers[legacy]
			if enabled == pluginActive {
				return fmt.Errorf("%s has inconsistent legacy ownership", file)
			}
		}
	}
	if r.Direction == Reverse {
		if err := plainPath(filepath.Join(dir, templateFile)); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, templateFile))
		if err != nil || !evidenceHash.MatchString(r.TemplateHash) || workspace.ConfigHash(data) != r.TemplateHash {
			return errors.New("legacy template snapshot/hash mismatch")
		}
	}
	if r.Ledger.ObservedEnd < 0 {
		return errors.New("invalid ledger snapshot")
	}
	hash, n, err := events.PrefixHash(ws, r.Ledger.ObservedEnd)
	if err != nil || n < r.Ledger.ObservedEnd || hash != r.Ledger.EndHash {
		return errors.New("receipt ledger prefix no longer validates")
	}
	if len(r.Sources) != len(r.Map) || len(r.Destinations) != len(r.Map) || len(r.Ledger.Pending) != len(r.Map) {
		return errors.New("incomplete checkpoint evidence")
	}
	sources := map[string]cursorEvidence{}
	destinations := map[string]cursorEvidence{}
	for _, group := range []struct {
		values []cursorEvidence
		into   map[string]cursorEvidence
	}{{r.Sources, sources}, {r.Destinations, destinations}} {
		for _, e := range group.values {
			if _, duplicate := group.into[e.Identity]; duplicate {
				return errors.New("duplicate checkpoint identity")
			}
			group.into[e.Identity] = e
			if !e.Existed {
				if e.Raw != "" || e.Position != 0 || e.PrefixHash != "" {
					return errors.New("absent checkpoint has content")
				}
				continue
			}
			cp, err := handlers.ParseCheckpoint([]byte(e.Raw))
			if err != nil || cp.Position != e.Position || cp.PrefixHash != e.PrefixHash {
				return errors.New("checkpoint bytes/evidence mismatch")
			}
			if err := handlers.ValidateCheckpoint(ws, cp); err != nil {
				return err
			}
		}
	}
	for namespaced, legacy := range r.Map {
		source, dest := legacy, namespaced
		if r.Direction == Reverse {
			source, dest = dest, source
		}
		s, ok := sources[source]
		if !ok || !s.Existed {
			return errors.New("missing source evidence")
		}
		d, ok := destinations[dest]
		if !ok || d.Position > s.Position {
			return errors.New("invalid destination evidence")
		}
		pending, ok := r.Ledger.Pending[dest]
		if !ok || s.Position > r.Ledger.ObservedEnd || pending != [2]int{s.Position, r.Ledger.ObservedEnd} {
			return errors.New("invalid pending range")
		}
	}
	return nil
}

func validateCommitted(path string, r preparedReceipt) error {
	if err := plainPath(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c committedReceipt
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Version != receiptVersion || c.AttemptID != r.AttemptID || c.Publication.Hash != r.TargetConfigHash || !c.Publication.Report.Renamed || len(c.Transfers) != len(r.Sources) {
		return errors.New("committed receipt does not match prepared evidence")
	}
	for i, source := range r.Sources {
		dest := r.Map[source.Identity]
		if r.Direction == Forward {
			dest = r.Plugin + "/" + source.Identity
		}
		expected := Transfer{Source: source.Identity, Destination: dest, Position: source.Position, PrefixHash: source.PrefixHash, ObservedEnd: r.Ledger.ObservedEnd, DestinationHadCheckpoint: r.Destinations[i].Existed}
		if !reflect.DeepEqual(c.Transfers[i], expected) {
			return errors.New("committed transfer evidence mismatch")
		}
	}
	return nil
}
