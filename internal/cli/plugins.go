package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/registry"
	"gopkg.in/yaml.v3"
)

func newPluginCmd() *cobra.Command {
	command := &cobra.Command{Use: "plugin", Short: "Install and enable trusted Docket plugins"}
	command.AddCommand(newPluginAddCmd(), newPluginListCmd(), newPluginRemoveCmd(), newPluginUpdateCmd(), newPluginEnableCmd(), newPluginDisableCmd())
	return command
}

func newPluginAddCmd() *cobra.Command {
	var name string
	command := &cobra.Command{
		Use: "add PATH|OWNER/REPO[@REF]", Short: "Install a linked local plugin or managed git plugin", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := pluginmgr.Add(args[0], name, Version)
			if err != nil {
				return err
			}
			if builtinCommand(entry.Name) {
				_, _ = pluginmgr.Remove(entry.Name)
				return fmt.Errorf("plugin name %q collides with a builtin docket command", entry.Name)
			}
			if flagJSON {
				return printJSON(entry)
			}
			fmt.Printf("Installed plugin %s %s at %s\n", entry.Name, entry.Version, entry.Path)
			return nil
		},
	}
	command.Flags().StringVar(&name, "name", "", "require this manifest name")
	return command
}

func newPluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List instance-installed plugins", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := registry.Load()
			if err != nil {
				return err
			}
			if flagJSON {
				values := config.Plugins
				if values == nil {
					values = []registry.PluginEntry{}
				}
				return printJSON(values)
			}
			if len(config.Plugins) == 0 {
				fmt.Println("No plugins installed.")
				return nil
			}
			writer := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(writer, "NAME\tVERSION\tSOURCE\tPATH")
			for _, entry := range config.Plugins {
				source := entry.Source.Type
				if entry.Source.Ref != "" {
					source += "@" + entry.Source.Ref
				}
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", entry.Name, entry.Version, source, entry.Path)
			}
			return writer.Flush()
		},
	}
}

func newPluginRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove NAME", Short: "Unregister a plugin", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			removed, err := pluginmgr.Remove(args[0])
			if err != nil {
				return err
			}
			if !removed {
				return fmt.Errorf("plugin %q is not installed", args[0])
			}
			if flagJSON {
				return printJSON(map[string]string{"removed": args[0]})
			}
			fmt.Printf("Removed plugin %s\n", args[0])
			return nil
		},
	}
}

func newPluginUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use: "update [NAME]", Short: "Validate and atomically update managed git plugins", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			entries, err := pluginmgr.Update(name, Version)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(entries)
			}
			for _, entry := range entries {
				if entry.Source.Type == "local" {
					fmt.Printf("%s is linked; update it in %s\n", entry.Name, entry.Path)
				} else {
					fmt.Printf("Updated %s to %s (%s)\n", entry.Name, entry.Version, entry.Source.Ref)
				}
			}
			return nil
		},
	}
}

func newPluginEnableCmd() *cobra.Command {
	var workspacePath string
	var adopt bool
	var fromStart bool
	var settings []string
	var expectHash string
	var receiptDir string
	command := &cobra.Command{
		Use: "enable NAME", Short: "Enable an installed plugin for a workspace", Args: cobra.ExactArgs(1),
		Long: `Enable an installed plugin for a workspace.

A plain enable seeds missing plugin-handler cursors at the current log end.
--from-start writes explicit zero cursors so the whole log replays.
--adopt-cursors runs the ownership handoff: it quiesces the legacy and plugin
identities, transfers each legacy handler's validated checkpoint to its plugin
identity, removes the matching legacy declarations and publishes one config
change. Acknowledged events are not replayed and pending ones stay pending.
An existing --receipt-dir is inspected and reported; it is never re-applied.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if adopt && fromStart {
				return fmt.Errorf("--adopt-cursors and --from-start are mutually exclusive")
			}
			values, err := parseSettings(settings)
			if err != nil {
				return err
			}
			if (expectHash != "" || receiptDir != "") && !adopt {
				return fmt.Errorf("--expect-config-sha256 and --receipt-dir require --adopt-cursors")
			}
			if adopt {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				result, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{
					Context: ctx, WorkspacePath: workspacePath, Plugin: args[0], Direction: pluginmgr.Forward,
					Values: values, ExpectConfigHash: expectHash, ReceiptDir: receiptDir, EngineVersion: Version, ReceiptAllocated: reportReceiptPath,
				})
				return reportHandoff(result, err)
			}
			if err := pluginmgr.Enable(workspacePath, args[0], values, false, fromStart, Version); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"enabled": args[0], "workspace": workspacePath})
			}
			fmt.Printf("Enabled plugin %s\n", args[0])
			return nil
		},
	}
	command.Flags().StringVar(&workspacePath, "workspace", ".", "workspace path")
	command.Flags().BoolVar(&adopt, "adopt-cursors", false, "transfer same-named legacy handler checkpoints to the plugin and remove legacy wiring")
	command.Flags().BoolVar(&fromStart, "from-start", false, "replay the existing event log")
	command.Flags().StringArrayVar(&settings, "set", nil, "workspace config key=value (repeatable)")
	command.Flags().StringVar(&expectHash, "expect-config-sha256", "", "require the current declared config.yaml to have this SHA-256 (with --adopt-cursors)")
	command.Flags().StringVar(&receiptDir, "receipt-dir", "", "new private attempt directory for handoff receipts; an existing directory is inspected only (with --adopt-cursors)")
	return command
}

func newPluginDisableCmd() *cobra.Command {
	var workspacePath string
	var adopt bool
	var legacyConfig string
	var expectHash string
	var receiptDir string
	command := &cobra.Command{
		Use: "disable NAME", Short: "Disable a plugin without deleting its cursors", Args: cobra.ExactArgs(1),
		Long: `Disable a plugin for a workspace.

A plain disable removes the plugin declaration and keeps its cursors. It is an
opt-out, not recovery: nothing transfers plugin progress back to legacy
handlers, so restoring legacy handler wiring afterwards replays the events the
plugin already acknowledged.

--adopt-cursors runs the reverse ownership handoff instead. It requires a
reviewed --legacy-config template declaring each mapped legacy handler,
--expect-config-sha256 of the current config.yaml and a new --receipt-dir.
Only the mapped handler declarations are imported from the template; statuses
the plugin contributed are pinned at their current positions. An existing
--receipt-dir is inspected and reported; it is never re-applied.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !adopt {
				if legacyConfig != "" || expectHash != "" || receiptDir != "" {
					return fmt.Errorf("--legacy-config, --expect-config-sha256 and --receipt-dir require --adopt-cursors")
				}
				if err := pluginmgr.Disable(workspacePath, args[0]); err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]any{"disabled": args[0], "workspace": workspacePath, "cursors_transferred": false})
				}
				fmt.Printf("Disabled plugin %s (declaration removed; no cursor handoff)\n", args[0])
				return nil
			}
			var template []byte
			if legacyConfig != "" {
				data, err := os.ReadFile(legacyConfig)
				if err != nil {
					return fmt.Errorf("read --legacy-config: %w", err)
				}
				template = data
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			result, err := pluginmgr.Handoff(pluginmgr.HandoffRequest{
				Context: ctx, WorkspacePath: workspacePath, Plugin: args[0], Direction: pluginmgr.Reverse,
				LegacyTemplate: template, ExpectConfigHash: expectHash, ReceiptDir: receiptDir, EngineVersion: Version, ReceiptAllocated: reportReceiptPath,
			})
			return reportHandoff(result, err)
		},
	}
	command.Flags().StringVar(&workspacePath, "workspace", ".", "workspace path")
	command.Flags().BoolVar(&adopt, "adopt-cursors", false, "transfer plugin handler checkpoints back to legacy handlers declared in --legacy-config")
	command.Flags().StringVar(&legacyConfig, "legacy-config", "", "reviewed legacy config template; only mapped handler declarations are imported")
	command.Flags().StringVar(&expectHash, "expect-config-sha256", "", "require the current declared config.yaml to have this SHA-256")
	command.Flags().StringVar(&receiptDir, "receipt-dir", "", "new private attempt directory for handoff receipts; an existing directory is inspected only")
	return command
}

func reportReceiptPath(path string) { fmt.Fprintf(os.Stderr, "Handoff receipt: %s\n", path) }

// reportHandoff prints the exported summary (hashes and paths only) for both a
// successful transition and a classified failure, then returns the error.
func reportHandoff(result pluginmgr.HandoffResult, err error) error {
	if flagJSON {
		if printErr := printJSON(result); printErr != nil {
			return printErr
		}
		return err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Handoff %s: %s\n", result.Status, strings.Join(result.Diagnosis, "; "))
		if result.ReceiptDir != "" {
			fmt.Fprintf(os.Stderr, "Receipt: %s\n", result.ReceiptDir)
		}
		return err
	}
	fmt.Printf("Handoff %s (%s) for plugin %s\n", result.Status, result.Direction, result.Plugin)
	if result.ReceiptDir != "" {
		fmt.Printf("Receipt: %s\n", result.ReceiptDir)
	}
	for _, transfer := range result.Transfers {
		fmt.Printf("  %s -> %s at %d (pending %d..%d)\n", transfer.Source, transfer.Destination, transfer.Position, transfer.Position, transfer.ObservedEnd)
	}
	if result.Status == pluginmgr.StatusCommitted {
		fmt.Printf("Config %s -> %s (power-loss durable: %t)\n", short(result.BeforeConfigHash), short(result.TargetConfigHash), result.PowerLossDurable)
	}
	for _, note := range result.Diagnosis {
		fmt.Printf("  note: %s\n", note)
	}
	return nil
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func parseSettings(entries []string) (map[string]any, error) {
	result := map[string]any{}
	for _, entry := range entries {
		key, raw, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("--set must be key=value")
		}
		var value any
		if err := yaml.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("--set %s: %w", key, err)
		}
		result[key] = value
	}
	return result, nil
}

func builtinCommand(name string) bool {
	_, exists := map[string]struct{}{
		"__lua-hook": {}, "attach": {}, "attach-file": {}, "comment": {}, "completion": {}, "context": {},
		"detach": {}, "edit": {}, "events": {}, "files": {}, "help": {}, "inbox": {}, "init": {}, "label": {},
		"link": {}, "list": {}, "move": {}, "new": {}, "plugin": {}, "project": {}, "reference": {}, "reindex": {},
		"guide": {}, "ref": {}, "serve": {}, "service": {}, "session": {}, "show": {}, "skill": {}, "unlink": {}, "wait": {}, "watch": {}, "workspace": {},
	}[name]
	return exists
}
