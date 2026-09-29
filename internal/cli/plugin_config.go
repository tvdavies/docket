package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/pluginmgr"
	"gopkg.in/yaml.v3"
)

func newPluginConfigCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Show or update a plugin's instance, workspace and status settings",
		Long: `Plugin settings have three scopes:

  instance   machine-wide, stored in the Docket registry
  workspace  per workspace, stored in that workspace's config.yaml
  status     per workspace and composed status (lane)

"get" prints the schemas and stored values. Instance values include defaults;
workspace and status values are exactly what is stored. Secret fields are
never stored or printed; supply them through the hook environment.

"set" merges values into one scope. Keys you do not name are kept. A list or
map replaces the stored value whole. Empty strings, zero and false are stored
as real values. Every value is validated against the plugin's schema (and, for
instance scope, against every workspace that enables the plugin) before any
file is written; a rejected update changes nothing.`,
	}
	command.AddCommand(newPluginConfigGetCmd(), newPluginConfigSetCmd())
	return command
}

func newPluginConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get [NAME]",
		Short:   "Print plugin schemas and stored settings",
		Example: "  docket plugin config get\n  docket plugin config get dispatch --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			settings, err := pluginmgr.DescribeConfig(name)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(settings)
			}
			if len(settings) == 0 {
				fmt.Println("No plugins installed.")
				return nil
			}
			for index, entry := range settings {
				if index > 0 {
					fmt.Println()
				}
				fmt.Printf("%s %s\n", entry.Name, entry.Version)
				printSettingValues("  instance", entry.InstanceValues)
				names := make([]string, 0, len(entry.Workspaces))
				for workspaceName := range entry.Workspaces {
					names = append(names, workspaceName)
				}
				sort.Strings(names)
				for _, workspaceName := range names {
					values := entry.Workspaces[workspaceName]
					printSettingValues("  workspace "+workspaceName, values.Config)
					statuses := make([]string, 0, len(values.Statuses))
					for status := range values.Statuses {
						statuses = append(statuses, status)
					}
					sort.Strings(statuses)
					for _, status := range statuses {
						printSettingValues("  status "+workspaceName+"/"+status, values.Statuses[status])
					}
				}
			}
			return nil
		},
	}
}

func printSettingValues(label string, values map[string]any) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Printf("%s:", label)
	if len(keys) == 0 {
		fmt.Println(" (none)")
		return
	}
	fmt.Println()
	for _, key := range keys {
		encoded, _ := json.Marshal(values[key])
		fmt.Printf("    %s = %s\n", key, encoded)
	}
}

func newPluginConfigSetCmd() *cobra.Command {
	var scope, workspacePath, status, file string
	var settings []string
	command := &cobra.Command{
		Use:   "set NAME [KEY=VALUE...]",
		Short: "Validate and store plugin settings in one scope",
		Long: `Each KEY=VALUE value is parsed as YAML, so 3 is a number, true a boolean,
'"3"' a string and '[a, b]' a list. --file reads a JSON or YAML object of
values instead ("-" reads stdin). The scope defaults to workspace, or to
status when --status is given.`,
		Example: `  docket plugin config set dispatch --scope instance endpoint=http://127.0.0.1:7464
  docket plugin config set dispatch --workspace ~/dev/app server_root=/srv/app
  docket plugin config set dispatch --status merge agent=merger
  docket plugin config set dispatch --scope instance --file settings.json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := parseSettings(append(settings, args[1:]...))
			if err != nil {
				return err
			}
			if file != "" {
				fromFile, err := readSettingsFile(file)
				if err != nil {
					return err
				}
				for key, value := range fromFile {
					if _, duplicate := values[key]; duplicate {
						return fmt.Errorf("%s is set both in --file and on the command line", key)
					}
					values[key] = value
				}
			}
			if len(values) == 0 {
				return fmt.Errorf("no settings given; pass KEY=VALUE arguments or --file")
			}
			if scope == "" {
				scope = string(pluginmgr.ScopeWorkspace)
				if status != "" {
					scope = string(pluginmgr.ScopeStatus)
				}
			}
			target := pluginmgr.ConfigTarget{Plugin: args[0], Scope: pluginmgr.ConfigScope(scope), Status: status}
			if target.Scope != pluginmgr.ScopeInstance {
				target.WorkspacePath = workspacePath
			} else if cmd.Flags().Changed("workspace") {
				return fmt.Errorf("--workspace does not apply to instance scope")
			}
			if err := pluginmgr.SetConfig(target, values); err != nil {
				return err
			}
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if flagJSON {
				return printJSON(map[string]any{"plugin": args[0], "scope": scope, "status": status, "keys": keys})
			}
			// Only key names are echoed: values may be sensitive.
			fmt.Printf("Updated %s %s settings: %s\n", args[0], scope, strings.Join(keys, ", "))
			return nil
		},
	}
	command.Flags().StringVar(&scope, "scope", "", "instance, workspace or status (default workspace, or status with --status)")
	command.Flags().StringVar(&workspacePath, "workspace", ".", "workspace path for workspace and status scopes")
	command.Flags().StringVar(&status, "status", "", "composed status name for status scope")
	command.Flags().StringVar(&file, "file", "", "JSON or YAML object of settings (- for stdin)")
	command.Flags().StringArrayVar(&settings, "set", nil, "KEY=VALUE (repeatable; same as a positional argument)")
	return command
}

func readSettingsFile(path string) (map[string]any, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = readStdin()
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read --file: %w", err)
	}
	// YAML also parses JSON, and matches how KEY=VALUE arguments are typed.
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("--file must contain a JSON or YAML object: %w", err)
	}
	if values == nil {
		values = map[string]any{}
	}
	return values, nil
}
