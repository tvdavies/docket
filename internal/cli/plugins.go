package cli

import (
	"fmt"
	"os"
	"strings"
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
	command := &cobra.Command{
		Use: "enable NAME", Short: "Enable an installed plugin for a workspace", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := parseSettings(settings)
			if err != nil {
				return err
			}
			if err := pluginmgr.Enable(workspacePath, args[0], values, adopt, fromStart, Version); err != nil {
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
	command.Flags().BoolVar(&adopt, "adopt-cursors", false, "copy same-named legacy handler cursors and remove legacy wiring")
	command.Flags().BoolVar(&fromStart, "from-start", false, "replay the existing event log")
	command.Flags().StringArrayVar(&settings, "set", nil, "workspace config key=value (repeatable)")
	return command
}

func newPluginDisableCmd() *cobra.Command {
	var workspacePath string
	command := &cobra.Command{
		Use: "disable NAME", Short: "Disable a plugin without deleting its cursors", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := pluginmgr.Disable(workspacePath, args[0]); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"disabled": args[0], "workspace": workspacePath})
			}
			fmt.Printf("Disabled plugin %s\n", args[0])
			return nil
		},
	}
	command.Flags().StringVar(&workspacePath, "workspace", ".", "workspace path")
	return command
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
