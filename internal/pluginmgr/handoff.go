package pluginmgr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
	"gopkg.in/yaml.v3"
)

// Direction selects which identity set currently owns delivery and which one
// takes over. Both directions share one transition.
type Direction string

const (
	// Forward transfers legacy workspace handlers h to plugin identities NAME/h
	// and enables the plugin.
	Forward Direction = "forward"
	// Reverse transfers plugin identities NAME/h back to legacy handlers h from
	// a reviewed template and disables the plugin.
	Reverse Direction = "reverse"
)

// Handoff result statuses describe both fresh publication outcomes and
// read-only inspection of an existing receipt directory.
const (
	StatusCommitted        = "committed"
	StatusAlreadyCommitted = "already_committed"
	StatusSourceActive     = "source_active"
	StatusTargetActive     = "target_active"
	StatusNeedsInspection  = "needs_inspection"
	StatusRejected         = "rejected"
)

const (
	receiptVersion       = 1
	preparedReceiptFile  = "prepared.json"
	committedReceiptFile = "committed.json"
	configBeforeFile     = "config-before.yaml"
	configTargetFile     = "config-target.yaml"
	templateFile         = "legacy-template.yaml"
)

// ErrHandoffRejected wraps every precondition failure that leaves the
// workspace untouched.
var ErrHandoffRejected = errors.New("handoff rejected")

// ErrReceiptExists reports that a fresh attempt named an existing receipt
// directory. Existing directories are inspection-only.
var ErrReceiptExists = errors.New("receipt directory already exists")

// HandoffRequest describes one ownership transition attempt.
type HandoffRequest struct {
	Context       context.Context
	WorkspacePath string
	Plugin        string
	Direction     Direction
	// Values are forward-only workspace plugin config values (--set).
	Values map[string]any
	// LegacyTemplate is reverse-only reviewed legacy config bytes. It is input
	// data: only the mapped handler declarations are imported from it.
	LegacyTemplate []byte
	// ExpectConfigHash must equal the SHA-256 of the current declared config.
	// Reverse attempts require it; forward attempts may omit it.
	ExpectConfigHash string
	// ReceiptDir is a create-once private attempt directory. Reverse attempts
	// require it; a forward attempt without one is allocated beneath workspace
	// handler state. An existing directory is inspected, never mutated.
	ReceiptDir    string
	EngineVersion string
	// ReceiptAllocated reports the attempt path before preparation or cursor
	// writes; the CLI sends this to stderr even when stdout is JSON.
	ReceiptAllocated func(string)
}

// Transfer is one identity checkpoint moved by the handoff.
type Transfer struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Position    int    `json:"position"`
	PrefixHash  string `json:"prefix_hash"`
	// ObservedEnd is the log length when the checkpoint was captured; events in
	// (Position, ObservedEnd] plus later appends remain pending for the
	// destination.
	ObservedEnd int `json:"observed_end"`
	// DestinationHadCheckpoint records that an inactive prior checkpoint was
	// overwritten; its original bytes are retained in the private receipt.
	DestinationHadCheckpoint bool `json:"destination_had_checkpoint"`
}

// HandoffResult is the exported summary. It contains hashes and paths, never
// configuration values.
type HandoffResult struct {
	Status            string                 `json:"status"`
	AttemptID         string                 `json:"attempt_id,omitempty"`
	Direction         Direction              `json:"direction,omitempty"`
	Plugin            string                 `json:"plugin"`
	WorkspaceRoot     string                 `json:"workspace_root"`
	ReceiptDir        string                 `json:"receipt_dir,omitempty"`
	BeforeConfigHash  string                 `json:"before_config_hash,omitempty"`
	TargetConfigHash  string                 `json:"target_config_hash,omitempty"`
	CurrentConfigHash string                 `json:"current_config_hash,omitempty"`
	Transfers         []Transfer             `json:"transfers,omitempty"`
	Publication       *workspace.Publication `json:"publication,omitempty"`
	// PowerLossDurable records successful file/directory syncs for the whole
	// preparation/publication sequence, not hardware or filesystem guarantees.
	PowerLossDurable bool     `json:"power_loss_durable"`
	Diagnosis        []string `json:"diagnosis,omitempty"`
}

// preparedReceipt is persisted before the first destination write. It is
// private (0600) because it embeds cursor bytes and references raw config
// snapshots stored beside it.
type preparedReceipt struct {
	Version          int                `json:"version"`
	AttemptID        string             `json:"attempt_id"`
	CreatedAt        string             `json:"created_at"`
	Direction        Direction          `json:"direction"`
	Plugin           string             `json:"plugin"`
	WorkspaceRoot    string             `json:"workspace_root"`
	RegistryPath     string             `json:"registry_path"`
	RegistrySHA256   string             `json:"registry_sha256"`
	Map              map[string]string  `json:"map"`
	BeforeConfigHash string             `json:"before_config_hash"`
	TargetConfigHash string             `json:"target_config_hash"`
	TemplateHash     string             `json:"template_hash,omitempty"`
	Provenance       handoffProvenance  `json:"provenance"`
	Sources          []cursorEvidence   `json:"sources"`
	Destinations     []cursorEvidence   `json:"destinations"`
	Ledger           ledgerSnapshot     `json:"ledger"`
	Scripts          []scriptProvenance `json:"scripts"`
}

type committedReceipt struct {
	Version     int                   `json:"version"`
	AttemptID   string                `json:"attempt_id"`
	CommittedAt string                `json:"committed_at"`
	Publication workspace.Publication `json:"publication"`
	Transfers   []Transfer            `json:"transfers"`
}

type handoffProvenance struct {
	EngineVersion     string `json:"engine_version"`
	CommandExecutable string `json:"command_executable"`
	CommandSHA256     string `json:"command_sha256"`
	GoVersion         string `json:"go_version,omitempty"`
	VCSRevision       string `json:"vcs_revision,omitempty"`
	VCSModified       string `json:"vcs_modified,omitempty"`
	// ServiceBinary is never verified by the command itself; an operational
	// packet must attest the running service separately.
	ServiceBinary        string                `json:"service_binary"`
	PluginPath           string                `json:"plugin_path"`
	PluginVersion        string                `json:"plugin_version"`
	PluginSource         registry.PluginSource `json:"plugin_source"`
	ManifestSHA256       string                `json:"manifest_sha256"`
	InstanceConfigSHA256 string                `json:"instance_config_sha256"`
}

type cursorEvidence struct {
	Identity   string `json:"identity"`
	Existed    bool   `json:"existed"`
	Raw        string `json:"raw,omitempty"`
	Position   int    `json:"position"`
	PrefixHash string `json:"prefix_hash"`
}

type ledgerSnapshot struct {
	ObservedEnd int               `json:"observed_end"`
	EndHash     string            `json:"end_hash"`
	Pending     map[string][2]int `json:"pending"`
}

type scriptProvenance struct {
	Identity string `json:"identity"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256,omitempty"`
	Missing  bool   `json:"missing,omitempty"`
}

// crashHook lets fixture subprocesses terminate at exact commit boundaries.
// Production never sets it.
var crashHook func(stage string)

func crashPoint(stage string) {
	if crashHook != nil {
		crashHook(stage)
	}
}

// Handoff runs one ownership transition or inspects an existing receipt.
func Handoff(request HandoffRequest) (HandoffResult, error) {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	result := HandoffResult{Plugin: request.Plugin, Direction: request.Direction}
	if os.Getenv("DOCKET_HANDLER_STACK") != "" {
		return reject(result, "handoff must not run from inside a handler invocation")
	}
	switch request.Direction {
	case Forward, Reverse:
	default:
		return reject(result, fmt.Sprintf("unknown direction %q", request.Direction))
	}
	root, err := workspace.FindRootAt(request.WorkspacePath)
	if err != nil {
		return reject(result, err.Error())
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return reject(result, err.Error())
	}
	result.WorkspaceRoot = root

	if request.ReceiptDir != "" {
		receiptDir, err := filepath.Abs(request.ReceiptDir)
		if err != nil {
			return reject(result, err.Error())
		}
		request.ReceiptDir = receiptDir
		if err := plainPath(receiptDir); err != nil {
			return reject(result, err.Error())
		}
		if info, err := os.Lstat(receiptDir); err == nil {
			if !info.IsDir() {
				return reject(result, "receipt path exists and is not a directory")
			}
			return inspectReceipt(root, receiptDir, result)
		} else if !os.IsNotExist(err) {
			return reject(result, err.Error())
		}
	}
	if request.Direction == Reverse {
		if request.ReceiptDir == "" {
			return reject(result, "reverse handoff requires --receipt-dir naming a new directory")
		}
		if request.ExpectConfigHash == "" {
			return reject(result, "reverse handoff requires --expect-config-sha256")
		}
		if len(request.LegacyTemplate) == 0 {
			return reject(result, "reverse handoff requires --legacy-config template bytes")
		}
	}
	if request.Direction == Forward && len(request.LegacyTemplate) != 0 {
		return reject(result, "forward handoff derives legacy declarations from the current config; --legacy-config is reverse-only")
	}
	if request.Direction == Reverse && len(request.Values) != 0 {
		return reject(result, "--set is forward-only")
	}

	// Pre-lock discovery: the manifest determines which identities to lock.
	entry, manifest, err := installedManifest(request.Plugin, request.EngineVersion)
	if err != nil {
		return reject(result, err.Error())
	}
	mapping := identityMap(manifest)
	if len(mapping) == 0 {
		return reject(result, fmt.Sprintf("plugin %q declares no handlers; nothing to hand off", request.Plugin))
	}
	lockNames := make([]string, 0, 2*len(mapping))
	for namespaced, legacy := range mapping {
		lockNames = append(lockNames, namespaced, legacy)
	}

	ws := &workspace.Workspace{Root: root}
	if err := validateStatePaths(ws, mapping); err != nil {
		return reject(result, err.Error())
	}
	registryPath, err := registry.ConfigPath()
	if err != nil {
		return reject(result, err.Error())
	}
	if err := plainPath(registryPath); err != nil {
		return reject(result, err.Error())
	}
	if err := plainPath(registryPath + ".lock"); err != nil {
		return reject(result, err.Error())
	}
	if err := checkReceiptPath(ws, request.ReceiptDir); err != nil {
		return reject(result, err.Error())
	}

	var outcome HandoffResult
	err = handlers.WithHandlerLocks(ctx, ws, lockNames, func() error {
		return workspace.WithDeclaredConfigTransaction(ctx, root, func(tx *workspace.ConfigTransaction) error {
			return registry.WithReadLock(ctx, func(reg *registry.Config) error {
				var runErr error
				outcome, runErr = runLocked(ctx, request, ws, tx, reg, entry, manifest, mapping)
				return runErr
			})
		})
	})
	if err != nil {
		if outcome.Status == "" {
			outcome = result
			outcome.Status = StatusRejected
			reason := safeHandoffDiagnosis(err.Error())
			outcome.Diagnosis = append(outcome.Diagnosis, reason)
			if reason != err.Error() {
				err = errors.New(reason)
			}
		}
		return outcome, err
	}
	return outcome, nil
}

func safeHandoffDiagnosis(reason string) string {
	// YAML type errors can quote arbitrary config scalars. Do not export them
	// in a summary or CLI error; raw inputs remain private inspection material.
	if strings.Contains(reason, "yaml:") {
		return "invalid YAML input; inspect the config, registry, manifest or template privately"
	}
	return reason
}

func reject(result HandoffResult, reason string) (HandoffResult, error) {
	reason = safeHandoffDiagnosis(reason)
	result.Status = StatusRejected
	result.Diagnosis = append(result.Diagnosis, reason)
	return result, fmt.Errorf("%w: %s", ErrHandoffRejected, reason)
}

func installedManifest(name, engineVersion string) (registry.PluginEntry, *plugin.Manifest, error) {
	config, err := registry.Load()
	if err != nil {
		return registry.PluginEntry{}, nil, err
	}
	for _, entry := range config.Plugins {
		if entry.Name == name {
			manifest, err := plugin.Load(entry.Path, engineVersion)
			if err != nil {
				return registry.PluginEntry{}, nil, err
			}
			if manifest.Name != name {
				return registry.PluginEntry{}, nil, fmt.Errorf("registry name %q does not match manifest name %q", name, manifest.Name)
			}
			return entry, manifest, nil
		}
	}
	return registry.PluginEntry{}, nil, fmt.Errorf("plugin %q is not installed", name)
}

func identityMap(manifest *plugin.Manifest) map[string]string {
	mapping := make(map[string]string, len(manifest.Handlers))
	for name := range manifest.Handlers {
		mapping[manifest.Name+"/"+name] = name
	}
	return mapping
}

// checkReceiptPath rejects a new receipt directory that would alias or
// contain workspace data. The directory itself must not exist (create-once),
// so only its parent can alias through a symlink; the parent is resolved and
// checked against the same protected paths.
func checkReceiptPath(ws *workspace.Workspace, receiptDir string) error {
	if receiptDir == "" {
		return nil
	}
	parent := filepath.Dir(filepath.Clean(receiptDir))
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("receipt parent directory: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(ws.Root)
	if err != nil {
		return err
	}
	for _, candidate := range []string{filepath.Clean(receiptDir), filepath.Join(resolved, filepath.Base(receiptDir))} {
		for _, root := range []string{ws.Root, resolvedRoot} {
			protected := &workspace.Workspace{Root: root}
			for _, path := range []string{root, protected.Path("config.yaml"), protected.EventsFile(), protected.HandlerStateDir(), protected.TasksDir(), protected.ProjectsDir()} {
				if candidate == filepath.Clean(path) {
					return fmt.Errorf("receipt directory must not be %s", path)
				}
			}
			if pathInside(candidate, root) {
				return errors.New("receipt directory must not contain the workspace")
			}
			if pathInside(protected.HandlerStateDir(), candidate) {
				return errors.New("explicit receipt directory must stay outside handler state")
			}
			if pathInside(protected.TasksDir(), candidate) || pathInside(protected.ProjectsDir(), candidate) {
				return errors.New("receipt directory must not live inside task or project data")
			}
		}
	}
	return nil
}

// runLocked executes the transition with every lock held. Returning an error
// before the first destination write leaves the workspace untouched.
func runLocked(ctx context.Context, request HandoffRequest, ws *workspace.Workspace, tx *workspace.ConfigTransaction, reg *registry.Config, entry registry.PluginEntry, manifest *plugin.Manifest, mapping map[string]string) (HandoffResult, error) {
	result := HandoffResult{Plugin: request.Plugin, Direction: request.Direction, WorkspaceRoot: ws.Root, BeforeConfigHash: tx.Hash, CurrentConfigHash: tx.Hash}
	fail := func(format string, args ...any) (HandoffResult, error) {
		return reject(result, fmt.Sprintf(format, args...))
	}
	if request.ExpectConfigHash != "" && !strings.EqualFold(request.ExpectConfigHash, tx.Hash) {
		return fail("declared config hash %s does not match expected %s", tx.Hash, request.ExpectConfigHash)
	}

	if err := validateStatePaths(ws, mapping); err != nil {
		return fail("%v", err)
	}
	// Re-read the registry and manifest under the locks; the mapping must be
	// exactly the set whose locks are held.
	var lockedEntry *registry.PluginEntry
	for index := range reg.Plugins {
		if reg.Plugins[index].Name == request.Plugin {
			lockedEntry = &reg.Plugins[index]
		}
	}
	if lockedEntry == nil {
		return fail("plugin %q was removed from the registry", request.Plugin)
	}
	if lockedEntry.Path != entry.Path || lockedEntry.Source != entry.Source {
		return fail("plugin %q registration changed while acquiring locks", request.Plugin)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(lockedEntry.Path, plugin.ManifestFile))
	if err != nil {
		return fail("%v", err)
	}
	lockedManifest, err := plugin.Load(lockedEntry.Path, request.EngineVersion)
	if err != nil {
		return fail("%v", err)
	}
	if !reflect.DeepEqual(identityMap(lockedManifest), mapping) {
		return fail("plugin %q handler set changed while acquiring locks", request.Plugin)
	}
	manifest = lockedManifest
	manifestAfterLoad, err := os.ReadFile(filepath.Join(manifest.Root, plugin.ManifestFile))
	if err != nil || workspace.ConfigHash(manifestAfterLoad) != workspace.ConfigHash(manifestBytes) {
		return fail("plugin manifest changed while loading")
	}
	registryPath, err := registry.ConfigPath()
	if err != nil {
		return fail("%v", err)
	}

	registryBytes, err := os.ReadFile(registryPath)
	if err != nil {
		return fail("capture registry: %v", err)
	}
	declared := tx.Config
	_, pluginEnabled := declared.Plugins.Values[request.Plugin]
	beforeEffective, _, err := composeWithOverride(declared, manifest, lockedEntry.Config)
	if err != nil {
		return fail("current config does not compose: %v", err)
	}

	sources := map[string]string{} // source identity -> destination identity
	legacyDecl := map[string]workspace.HandlerConfig{}
	var templateHash string
	target, err := workspace.CloneDeclared(&workspace.Workspace{DeclaredConfig: declared})
	if err != nil {
		return fail("%v", err)
	}

	switch request.Direction {
	case Forward:
		if pluginEnabled {
			return fail("plugin %q is already enabled: destination identities are active", request.Plugin)
		}
		for namespaced, legacy := range mapping {
			cfg, ok := declared.Handlers[legacy]
			if !ok {
				return fail("legacy handler %q is not declared; forward adoption requires every mapped legacy handler", legacy)
			}
			legacyDecl[legacy] = cfg
			sources[legacy] = namespaced
		}
		use := target.Plugins.Values[request.Plugin]
		if use.Config == nil {
			use.Config = map[string]any{}
		}
		for key, value := range request.Values {
			use.Config[key] = value
		}
		if field, ok := manifest.Config.Workspace["server_root"]; ok && field.Required {
			if _, exists := use.Config["server_root"]; !exists {
				use.Config["server_root"] = manifest.Root
			}
		}
		if err := workspace.EnablePlugin(target, manifest, use, true); err != nil {
			return fail("compose target config: %v", err)
		}
		// Contributed status pins are removed only when the manifest anchors
		// reproduce the current effective order; otherwise the explicit pins are
		// kept so the handoff never moves a lane.
		if composed, _, err := composeWithOverride(target, manifest, lockedEntry.Config); err != nil || !slices.Equal(composed.Statuses, beforeEffective.Statuses) {
			target.Statuses = append([]string(nil), declared.Statuses...)
			target.Terminal = append([]string(nil), declared.Terminal...)
		}
	case Reverse:
		if !pluginEnabled {
			return fail("plugin %q is not enabled: source identities are inactive", request.Plugin)
		}
		template, err := parseTemplate(request.LegacyTemplate)
		if err != nil {
			return fail("legacy template: %v", err)
		}
		templateHash = workspace.ConfigHash(request.LegacyTemplate)
		for name, cfg := range template.Handlers {
			mapped := false
			for _, legacy := range mapping {
				if legacy == name {
					mapped = true
				}
			}
			if mapped {
				continue
			}
			if existing, ok := declared.Handlers[name]; !ok || !sameHandler(existing, cfg) {
				return fail("legacy template declares unmapped handler %q; only mapped handlers may be imported", name)
			}
		}
		for namespaced, legacy := range mapping {
			cfg, ok := template.Handlers[legacy]
			if !ok {
				return fail("legacy template does not declare handler %q", legacy)
			}
			if _, active := declared.Handlers[legacy]; active {
				return fail("legacy handler %q is already declared while plugin %q is enabled: mixed active wiring", legacy, request.Plugin)
			}
			if err := checkRelativePath(cfg); err != nil {
				return fail("legacy template handler %q: %v", legacy, err)
			}
			legacyDecl[legacy] = cfg
			sources[namespaced] = legacy
		}
		if target.Handlers == nil {
			target.Handlers = map[string]workspace.HandlerConfig{}
		}
		for legacy, cfg := range legacyDecl {
			target.Handlers[legacy] = cfg
		}
		workspace.DisablePlugin(target, request.Plugin)
		pinContributedStatuses(target, beforeEffective, manifest)
	}

	// Validate the legacy declarations against the manifest contract.
	for namespaced, legacy := range mapping {
		if err := checkRelativePath(legacyDecl[legacy]); err != nil {
			return fail("legacy handler %q: %v", legacy, err)
		}
		handler := manifest.Handlers[strings.TrimPrefix(namespaced, request.Plugin+"/")]
		if err := equivalentHandler(legacyDecl[legacy], handler); err != nil {
			return fail("legacy handler %q does not match plugin handler %q: %v", legacy, namespaced, err)
		}
	}

	// Validate target composition fully before any cursor write.
	targetBytes, err := workspace.SerializeDeclaredConfig(target)
	if err != nil {
		return fail("target config: %v", err)
	}
	afterEffective, _, err := composeWithOverride(target, manifest, lockedEntry.Config)
	if err != nil {
		return fail("target config does not compose: %v", err)
	}
	if !slices.Equal(beforeEffective.Statuses, afterEffective.Statuses) {
		return fail("effective statuses would change from %v to %v", beforeEffective.Statuses, afterEffective.Statuses)
	}
	if !sameStringSet(beforeEffective.Terminal, afterEffective.Terminal) {
		return fail("effective terminal statuses would change from %v to %v", beforeEffective.Terminal, afterEffective.Terminal)
	}
	if err := unrelatedUnchanged(beforeEffective, afterEffective, request.Plugin, mapping); err != nil {
		return fail("%v", err)
	}
	for namespaced, legacy := range mapping {
		destination := legacy
		if request.Direction == Forward {
			destination = namespaced
		}
		if _, exists := afterEffective.Handlers[destination]; !exists {
			return fail("target config does not activate destination %q", destination)
		}
	}
	targetHash := workspace.ConfigHash(targetBytes)
	result.TargetConfigHash = targetHash

	// Capture fresh quiescent source checkpoints and validate destinations.
	projectRoot := filepath.Dir(ws.Root)
	// Read through EOF once, preserving read/scanner errors (Count alone
	// intentionally hides them for status displays).
	endHash, observedEnd, err := events.PrefixHash(ws, int(^uint(0)>>1))
	if err != nil {
		return fail("capture ledger snapshot: %v", err)
	}
	if observedEnd == 0 {
		endHash = ""
	}
	receipt := preparedReceipt{
		Version: receiptVersion, CreatedAt: now(), Direction: request.Direction, Plugin: request.Plugin,
		WorkspaceRoot: ws.Root, RegistryPath: registryPath, RegistrySHA256: workspace.ConfigHash(registryBytes), Map: mapping,
		BeforeConfigHash: tx.Hash, TargetConfigHash: targetHash, TemplateHash: templateHash,
		Provenance: provenance(request.EngineVersion, *lockedEntry, manifestBytes),
		Ledger:     ledgerSnapshot{ObservedEnd: observedEnd, EndHash: endHash, Pending: map[string][2]int{}},
	}
	receipt.Provenance.PluginVersion = manifest.Version
	if receipt.Provenance.CommandSHA256 == "" || receipt.Provenance.InstanceConfigSHA256 == "" {
		return fail("cannot capture command or instance-config provenance")
	}
	orderedSources := make([]string, 0, len(sources))
	for source := range sources {
		orderedSources = append(orderedSources, source)
	}
	sort.Strings(orderedSources)
	var transfers []Transfer
	for _, source := range orderedSources {
		destination := sources[source]
		checkpoint, err := handlers.ReadCheckpoint(ws, source)
		if err != nil {
			if errors.Is(err, handlers.ErrCheckpointMissing) {
				return fail("source %q has no checkpoint; drain it once so it acknowledges its position, or accept replay via an explicit --from-start enable instead of a handoff", source)
			}
			return fail("source %v", err)
		}
		if checkpoint.Position > observedEnd {
			return fail("source %q is beyond the captured ledger end", source)
		}
		receipt.Sources = append(receipt.Sources, cursorEvidence{Identity: source, Existed: true, Raw: string(checkpoint.Raw), Position: checkpoint.Position, PrefixHash: checkpoint.PrefixHash})
		evidence := cursorEvidence{Identity: destination}
		existing, err := handlers.ReadCheckpoint(ws, destination)
		switch {
		case err == nil:
			if existing.Position > checkpoint.Position {
				return fail("destination %q checkpoint %d is ahead of source %q checkpoint %d; refusing to guess", destination, existing.Position, source, checkpoint.Position)
			}
			evidence = cursorEvidence{Identity: destination, Existed: true, Raw: string(existing.Raw), Position: existing.Position, PrefixHash: existing.PrefixHash}
		case errors.Is(err, handlers.ErrCheckpointMissing):
		default:
			return fail("destination %v; refusing to overwrite an unreadable or corrupt inactive checkpoint without review", err)
		}
		receipt.Destinations = append(receipt.Destinations, evidence)
		receipt.Ledger.Pending[destination] = [2]int{checkpoint.Position, observedEnd}
		transfers = append(transfers, Transfer{Source: source, Destination: destination, Position: checkpoint.Position, PrefixHash: checkpoint.PrefixHash, ObservedEnd: observedEnd, DestinationHadCheckpoint: evidence.Existed})
	}
	for namespaced, legacy := range mapping {
		handler := manifest.Handlers[strings.TrimPrefix(namespaced, request.Plugin+"/")]
		receipt.Scripts = append(receipt.Scripts,
			scriptDigest(namespaced, manifest.Root, handler.Run, handler.Lua),
			scriptDigest(legacy, projectRoot, legacyDecl[legacy].Run, legacyDecl[legacy].Lua))
	}
	sort.Slice(receipt.Scripts, func(i, j int) bool { return receipt.Scripts[i].Identity < receipt.Scripts[j].Identity })
	for _, script := range receipt.Scripts {
		if script.Missing {
			return fail("handler %q script %s is missing", script.Identity, script.Path)
		}
		if err := plainPath(script.Path); err != nil {
			return fail("%v", err)
		}
		base := projectRoot
		if strings.HasPrefix(script.Identity, request.Plugin+"/") {
			base = manifest.Root
		}
		if !pathInside(base, script.Path) {
			return fail("script path escapes its root: %s", script.Path)
		}
		if pathInside(ws.Root, script.Path) {
			return fail("script path aliases workspace state: %s", script.Path)
		}
	}
	if err := plainPath(filepath.Join(manifest.Root, plugin.ManifestFile)); err != nil {
		return fail("%v", err)
	}

	// Allocate the private attempt directory and persist the prepared receipt.
	attemptID, err := newAttemptID()
	if err != nil {
		return fail("%v", err)
	}
	receipt.AttemptID = attemptID
	result.AttemptID = attemptID
	receiptDir := request.ReceiptDir
	if receiptDir == "" {
		receiptDir = filepath.Join(ws.HandlerStateDir(), "handoffs", attemptID)
		if err := plainPath(receiptDir); err != nil {
			return fail("%v", err)
		}
		if err := os.MkdirAll(filepath.Dir(receiptDir), 0o700); err != nil {
			return fail("%v", err)
		}
	}
	if err := os.Mkdir(receiptDir, 0o700); err != nil {
		if os.IsExist(err) {
			return fail("%v: %s", ErrReceiptExists, receiptDir)
		}
		return fail("create receipt directory: %v", err)
	}
	result.ReceiptDir = receiptDir
	if request.ReceiptAllocated != nil {
		request.ReceiptAllocated(receiptDir)
	}
	crashPoint("before_prepared")
	if err := writePrivate(filepath.Join(receiptDir, configBeforeFile), tx.Bytes); err != nil {
		return fail("%v", err)
	}
	if err := writePrivate(filepath.Join(receiptDir, configTargetFile), targetBytes); err != nil {
		return fail("%v", err)
	}
	if request.Direction == Reverse {
		if err := writePrivate(filepath.Join(receiptDir, templateFile), request.LegacyTemplate); err != nil {
			return fail("%v", err)
		}
	}
	if err := writePrivateJSON(filepath.Join(receiptDir, preparedReceiptFile), receipt); err != nil {
		return fail("%v", err)
	}
	syncThrough := filepath.Dir(receiptDir)
	if request.ReceiptDir == "" {
		syncThrough = ws.Root
	}
	if err := syncParents(receiptDir, syncThrough); err != nil {
		return fail("prepared receipt directory sync: %v", err)
	}
	crashPoint("after_prepared")

	// From here on the workspace is mutated. Destination cursors are inert
	// until the config publication; a failure leaves the source authoritative.
	result.Transfers = transfers
	for _, transfer := range transfers {
		crashPoint("before_destination:" + transfer.Destination)
		if err := handlers.WriteCheckpointLocked(ws, transfer.Destination, handlers.Checkpoint{Position: transfer.Position, PrefixHash: transfer.PrefixHash}); err != nil {
			result.Status = StatusSourceActive
			result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("destination %q write failed: %v; source remains active and receipt %s is inspection-only", transfer.Destination, err, receiptDir))
			return result, fmt.Errorf("prepare destination %q: %w", transfer.Destination, err)
		}
		crashPoint("after_destination:" + transfer.Destination)
	}
	for _, transfer := range transfers {
		if err := syncParents(filepath.Dir(filepath.Join(ws.HandlerStateDir(), transfer.Destination+".cursor")), ws.Root); err != nil {
			result.Status = StatusSourceActive
			result.Diagnosis = append(result.Diagnosis, "destination directory sync failed: "+err.Error())
			return result, err
		}
	}
	// Sources are locked so their checkpoints cannot have moved; re-validate
	// them against the log immediately before publication anyway, allowing
	// append-only growth.
	for _, transfer := range transfers {
		current, err := handlers.ReadCheckpoint(ws, transfer.Source)
		if err != nil || current.Position != transfer.Position || current.PrefixHash != transfer.PrefixHash {
			result.Status = StatusSourceActive
			result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("source %q changed before publication (%v); source remains active", transfer.Source, err))
			return result, fmt.Errorf("source %q changed before publication", transfer.Source)
		}
	}
	if err := ctx.Err(); err != nil {
		result.Status = StatusSourceActive
		result.Diagnosis = append(result.Diagnosis, "cancelled before publication; source remains active")
		return result, err
	}

	crashPoint("before_publish")
	if err := validateStatePaths(ws, mapping); err != nil {
		result.Status = StatusSourceActive
		result.Diagnosis = append(result.Diagnosis, err.Error())
		return result, err
	}
	if err := verifyCapturedFiles(tx, receipt); err != nil {
		result.Status = StatusSourceActive
		result.Diagnosis = append(result.Diagnosis, err.Error())
		return result, err
	}
	publication, err := tx.Publish(targetBytes, func(stage string) { crashPoint("config:" + stage) })
	if err != nil {
		if publication.Report.Renamed {
			result.Status = StatusNeedsInspection
		} else {
			result.Status = StatusSourceActive
		}
		result.Publication = &publication
		result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("config publication failed: %v", err))
		return result, err
	}
	result.Publication = &publication
	result.CurrentConfigHash = publication.Hash
	crashPoint("after_publish")
	if !publication.Report.DirSynced {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, "config renamed but directory sync failed; keep current wiring: "+publication.Report.DirSyncErr)
		return result, errors.New("config publication durability is uncertain")
	}
	committed := committedReceipt{Version: receiptVersion, AttemptID: attemptID, CommittedAt: now(), Publication: publication, Transfers: transfers}
	crashPoint("before_committed")
	if err := writePrivateJSON(filepath.Join(receiptDir, committedReceiptFile), committed); err != nil {
		result.Status = StatusTargetActive
		result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("config published but committed receipt failed: %v", err))
		return result, err
	}
	if err := store.SyncDir(receiptDir); err != nil {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, "committed receipt directory sync failed: "+err.Error())
		return result, err
	}
	crashPoint("after_committed")
	result.Status = StatusCommitted
	result.PowerLossDurable = true
	return result, nil
}

// inspectReceipt classifies an existing attempt directory without taking
// locks or writing anything. Values are a point-in-time snapshot.
func inspectReceipt(root, receiptDir string, result HandoffResult) (HandoffResult, error) {
	result.ReceiptDir = receiptDir
	if err := plainPath(filepath.Join(receiptDir, preparedReceiptFile)); err != nil {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, err.Error())
		return result, err
	}
	data, err := os.ReadFile(filepath.Join(receiptDir, preparedReceiptFile))
	if err != nil {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("existing receipt directory has no readable prepared receipt: %v; a fresh attempt must name a new directory", err))
		return result, fmt.Errorf("inspect receipt: %w", err)
	}
	var receipt preparedReceipt
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.Version != receiptVersion || receipt.AttemptID == "" || receipt.BeforeConfigHash == "" || receipt.TargetConfigHash == "" {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, "prepared receipt is corrupt or has an unsupported version")
		return result, errors.New("inspect receipt: prepared receipt is corrupt")
	}
	result.AttemptID = receipt.AttemptID
	result.Direction = receipt.Direction
	result.BeforeConfigHash = receipt.BeforeConfigHash
	result.TargetConfigHash = receipt.TargetConfigHash
	if receipt.WorkspaceRoot != root || receipt.Plugin != result.Plugin {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("receipt belongs to workspace %s plugin %q, not %s plugin %q", receipt.WorkspaceRoot, receipt.Plugin, root, result.Plugin))
		return result, errors.New("inspect receipt: receipt does not belong to this workspace and plugin")
	}
	ws := &workspace.Workspace{Root: root}
	if err := validateReceipt(ws, receiptDir, receipt); err != nil {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, "invalid receipt evidence: "+err.Error())
		return result, err
	}
	current, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, err.Error())
		return result, err
	}
	result.CurrentConfigHash = workspace.ConfigHash(current)
	if result.CurrentConfigHash == receipt.BeforeConfigHash {
		for _, source := range receipt.Sources {
			checkpoint, err := handlers.ReadCheckpoint(ws, source.Identity)
			if err != nil || checkpoint.Position < source.Position {
				result.Status = StatusNeedsInspection
				result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("active source %q is invalid or behind its recorded prefix", source.Identity))
				return result, errors.New("active source checkpoint needs inspection")
			}
		}
	}
	var invalidDestination bool
	for _, destination := range receipt.Destinations {
		checkpoint, err := handlers.ReadCheckpoint(ws, destination.Identity)
		if err != nil {
			if result.CurrentConfigHash == receipt.TargetConfigHash {
				invalidDestination = true
			}
			result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("destination %q: %v", destination.Identity, err))
			continue
		}
		if result.CurrentConfigHash == receipt.TargetConfigHash {
			for _, source := range receipt.Sources {
				dest := receipt.Map[source.Identity]
				if receipt.Direction == Forward {
					dest = receipt.Plugin + "/" + source.Identity
				}
				if dest == destination.Identity && checkpoint.Position < source.Position {
					invalidDestination = true
					result.Diagnosis = append(result.Diagnosis, "active destination is behind its transferred prefix")
				}
			}
		}
		result.Diagnosis = append(result.Diagnosis, fmt.Sprintf("destination %q checkpoint is %d (%s)", destination.Identity, checkpoint.Position, checkpoint.PrefixHash))
	}
	if invalidDestination {
		result.Status = StatusNeedsInspection
		return result, errors.New("active destination checkpoint is invalid")
	}
	if _, err := os.Lstat(filepath.Join(receiptDir, committedReceiptFile)); err == nil {
		if err := validateCommitted(filepath.Join(receiptDir, committedReceiptFile), receipt); err != nil {
			result.Status = StatusNeedsInspection
			result.Diagnosis = append(result.Diagnosis, "invalid committed receipt: "+err.Error())
			return result, err
		}
		result.Status = StatusAlreadyCommitted
		note := "committed receipt is historical; retrying it does not change ownership"
		if result.CurrentConfigHash != receipt.TargetConfigHash {
			note += "; the current config differs from this historical target; the receipt does not explain subsequent changes"
		}
		result.Diagnosis = append(result.Diagnosis, note)
		return result, nil
	} else if !os.IsNotExist(err) {
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, "cannot inspect committed receipt: "+err.Error())
		return result, err
	}
	switch result.CurrentConfigHash {
	case receipt.BeforeConfigHash:
		result.Status = StatusSourceActive
		result.Diagnosis = append(result.Diagnosis, "config hash equals the pre-attempt hash: treat the source set as active and any destination copies as inert; equality does not prove that no intervening round trip occurred, so a fresh attempt must use a new receipt directory and recapture checkpoints")
	case receipt.TargetConfigHash:
		result.Status = StatusTargetActive
		result.Diagnosis = append(result.Diagnosis, "config matches the target hash without a committed receipt: publication happened, the destination set is active; do not rewind destinations or restore the pre-attempt config")
	default:
		result.Status = StatusNeedsInspection
		result.Diagnosis = append(result.Diagnosis, "config matches neither receipt hash: keep current wiring and obtain a reviewed decision; this receipt cannot explain the drift")
	}
	return result, nil
}

func composeWithOverride(declared *workspace.Config, manifest *plugin.Manifest, instanceConfig map[string]any) (*workspace.Config, []workspace.LoadedPlugin, error) {
	return workspace.ComposeWithCandidate(declared, manifest, instanceConfig)
}

func parseTemplate(data []byte) (*workspace.Config, error) {
	var config workspace.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(config.Handlers) == 0 {
		return nil, errors.New("declares no handlers")
	}
	for name, handler := range config.Handlers {
		if handler.PluginName != "" || handler.PluginRoot != "" {
			return nil, fmt.Errorf("handler %q carries plugin runtime fields", name)
		}
	}
	probe := workspace.DefaultConfig()
	probe.Handlers = config.Handlers
	if err := probe.Validate(); err != nil {
		return nil, err
	}
	return &config, nil
}

func checkRelativePath(cfg workspace.HandlerConfig) error {
	path := cfg.Run
	if path == "" {
		path = cfg.Lua
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("path %q must be relative to the project root", path)
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if part == ".." {
			return fmt.Errorf("path %q must not traverse parent directories", path)
		}
	}
	return nil
}

func sameHandler(left, right workspace.HandlerConfig) bool {
	return left.Run == right.Run && left.Lua == right.Lua && reflect.DeepEqual(normalizeHandler(left), normalizeHandler(right))
}

type normalizedHandler struct {
	On       []string
	Match    string
	Runtime  string
	Delivery string
}

func normalizeHandler(cfg workspace.HandlerConfig) normalizedHandler {
	on := append([]string(nil), cfg.On...)
	sort.Strings(on)
	on = slices.Compact(on)
	match, _ := json.Marshal(cfg.Match)
	runtime := "run"
	if cfg.Lua != "" {
		runtime = "lua"
	}
	delivery := cfg.Delivery
	if delivery == "" {
		delivery = "inline"
	}
	return normalizedHandler{On: on, Match: string(match), Runtime: runtime, Delivery: delivery}
}

func equivalentHandler(legacy workspace.HandlerConfig, handler plugin.Handler) error {
	want := normalizeHandler(workspace.HandlerConfig{On: handler.On, Match: handler.Match, Run: handler.Run, Lua: handler.Lua, Delivery: handler.Delivery})
	got := normalizeHandler(legacy)
	if !slices.Equal(want.On, got.On) {
		return fmt.Errorf("event filters differ (%v vs %v)", got.On, want.On)
	}
	if want.Match != got.Match {
		return errors.New("match predicates differ")
	}
	if want.Runtime != got.Runtime {
		return fmt.Errorf("runtime differs (%s vs %s)", got.Runtime, want.Runtime)
	}
	if want.Delivery != got.Delivery {
		return fmt.Errorf("delivery class differs (%s vs %s)", got.Delivery, want.Delivery)
	}
	return nil
}

func pinContributedStatuses(target, effective *workspace.Config, manifest *plugin.Manifest) {
	contributed := map[string]bool{}
	for _, status := range manifest.Statuses {
		contributed[status.Name] = true
	}
	declared := map[string]bool{}
	for _, status := range target.Statuses {
		declared[status] = true
	}
	pinned := make([]string, 0, len(effective.Statuses))
	for _, status := range effective.Statuses {
		if declared[status] || contributed[status] {
			pinned = append(pinned, status)
		}
	}
	target.Statuses = pinned
	for _, status := range manifest.Statuses {
		if status.Terminal && !slices.Contains(target.Terminal, status.Name) {
			target.Terminal = append(target.Terminal, status.Name)
		}
	}
}

func sameStringSet(left, right []string) bool {
	l := append([]string(nil), left...)
	r := append([]string(nil), right...)
	sort.Strings(l)
	sort.Strings(r)
	return slices.Equal(slices.Compact(l), slices.Compact(r))
}

// unrelatedUnchanged proves that nothing outside the selected plugin and its
// mapped handlers differs between the two effective configs.
func unrelatedUnchanged(before, after *workspace.Config, name string, mapping map[string]string) error {
	involved := map[string]bool{}
	for namespaced, legacy := range mapping {
		involved[namespaced] = true
		involved[legacy] = true
	}
	strip := func(config *workspace.Config) *workspace.Config {
		copy := *config
		copy.Handlers = map[string]workspace.HandlerConfig{}
		for handlerName, cfg := range config.Handlers {
			if involved[handlerName] {
				continue
			}
			copy.Handlers[handlerName] = cfg
		}
		copy.Plugins = workspace.PluginUses{Values: map[string]workspace.PluginUse{}}
		for _, pluginName := range config.Plugins.Order {
			if pluginName == name {
				continue
			}
			copy.Plugins.Order = append(copy.Plugins.Order, pluginName)
			copy.Plugins.Values[pluginName] = config.Plugins.Values[pluginName]
		}
		copy.Statuses = nil
		copy.Terminal = nil
		return &copy
	}
	left, err := yaml.Marshal(strip(before))
	if err != nil {
		return err
	}
	right, err := yaml.Marshal(strip(after))
	if err != nil {
		return err
	}
	if string(left) != string(right) {
		return errors.New("target config changes configuration unrelated to the handoff")
	}
	if !reflect.DeepEqual(strip(before).Handlers, strip(after).Handlers) {
		return errors.New("target config changes unrelated handlers")
	}
	return nil
}

func scriptDigest(identity, base, run, lua string) scriptProvenance {
	path := run
	if path == "" {
		path = lua
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	record := scriptProvenance{Identity: identity, Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		record.Missing = true
		return record
	}
	sum := sha256.Sum256(data)
	record.SHA256 = hex.EncodeToString(sum[:])
	return record
}

func provenance(engineVersion string, entry registry.PluginEntry, manifestBytes []byte) handoffProvenance {
	manifestSum := sha256.Sum256(manifestBytes)
	instanceBytes, instanceErr := json.Marshal(entry.Config)
	instanceHash := ""
	if instanceErr == nil {
		instanceHash = workspace.ConfigHash(instanceBytes)
	}
	record := handoffProvenance{
		InstanceConfigSHA256: instanceHash,
		EngineVersion:        engineVersion, ServiceBinary: "unverified: attest the running service separately",
		PluginPath: entry.Path, PluginVersion: entry.Version, PluginSource: entry.Source,
		ManifestSHA256: hex.EncodeToString(manifestSum[:]),
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		record.GoVersion = info.GoVersion
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				record.VCSRevision = setting.Value
			case "vcs.modified":
				record.VCSModified = setting.Value
			}
		}
	}
	if executable, err := os.Executable(); err == nil {
		record.CommandExecutable = executable
		if data, err := os.ReadFile(executable); err == nil {
			sum := sha256.Sum256(data)
			record.CommandSHA256 = hex.EncodeToString(sum[:])
		}
	}
	return record
}

func newAttemptID() (string, error) {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buffer[:]), nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func writePrivate(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writePrivateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, append(data, '\n'))
}
