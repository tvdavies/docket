package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Inspect and manage registered workspace stores",
		Long: `docket init creates and registers the current workspace, so these commands
are mainly for inspection, validation, selection, manual registration, and
removal from the machine-wide service registry. Removing a registration never
removes task files.

Every command works on one workspace, chosen in this order:
  1. -w/--workspace NAME|PATH
  2. $DOCKET_HOME (set for handlers and plugins), then $DOCKET_WORKSPACE NAME|PATH
  3. the nearest .docket/ above the current directory
  4. the default saved by "docket workspace use NAME"
A registered name wins over a same-named directory; write ./NAME for the path.`,
		Example: `  docket -w client-b list               # one command, from any directory
  docket workspace use client-b         # default outside any workspace
  docket workspace current              # which workspace, and why
  cd (docket workspace path client-b)   # fish; bash: cd "$(docket workspace path client-b)"`,
	}
	cmd.AddCommand(newWorkspaceCheckCmd(), newWorkspaceAddCmd(), newWorkspaceListCmd(), newWorkspaceRemoveCmd(),
		newWorkspaceUseCmd(), newWorkspaceCurrentCmd(), newWorkspacePathCmd())
	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:     "add [PATH]",
		Short:   "Register an existing workspace with the service",
		Example: "  docket workspace add .\n  docket workspace add ~/dev/client-b --name client-b",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			entry, err := registry.Add(path, name)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(entry)
			}
			fmt.Printf("Registered %s at %s\n", entry.Name, entry.Path)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "route-safe name (defaults to the directory name)")
	return cmd
}

func newWorkspaceCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "check [PATH]",
		Short:   "Validate a workspace config and summarise its store",
		Long:    "PATH may be the project root, .docket directory, or any descendant; without PATH the selected workspace is checked. The command fails with the exact config parse or validation error.",
		Example: "  docket workspace check\n  docket workspace check ~/dev/client-b\n  docket -w client-b workspace check --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) == 1 {
				path = args[0]
			} else {
				resolution, err := resolveWorkspace()
				if err != nil {
					return err
				}
				path = resolution.Root
			}
			ws, err := workspace.OpenAt(path)
			if err != nil {
				return err
			}
			tasks, err := task.All(ws)
			if err != nil {
				return err
			}
			plugins := make([]string, 0, len(ws.Plugins))
			for _, loaded := range ws.Plugins {
				plugins = append(plugins, loaded.Manifest.Name)
			}
			summary := map[string]any{
				"workspace":     ws.Root,
				"statuses":      ws.Config.Statuses,
				"tasks":         len(tasks),
				"events":        events.Count(ws),
				"handlers":      ws.Config.HandlerNames(),
				"handler_count": len(ws.Config.Handlers),
				"plugins":       plugins,
			}
			if flagJSON {
				return printJSON(summary)
			}
			fmt.Printf("Workspace valid: %s\n", ws.Root)
			fmt.Printf("Statuses: %v\n", ws.Config.Statuses)
			fmt.Printf("Tasks: %d  Events: %d  Handlers: %d\n", len(tasks), events.Count(ws), len(ws.Config.Handlers))
			if len(plugins) > 0 {
				fmt.Printf("Plugins: %v\n", plugins)
				fmt.Printf("Handler identities: %v\n", ws.Config.HandlerNames())
			}
			return nil
		},
	}
}

// workspaceListEntry is one "workspace list --json" item. Current marks the
// workspace in effect for this invocation; Default marks the saved context.
type workspaceListEntry struct {
	registry.WorkspaceEntry
	Current bool `json:"current"`
	Default bool `json:"default"`
}

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List workspaces registered with the service",
		Long: `Columns are NAME, PATH and CURRENT. CURRENT is * for the workspace in effect
here (see "docket workspace current"). On a terminal the columns are aligned;
piped output is tab-separated.`,
		Example: "  docket workspace list\n  docket workspace list | cut -f1\n  docket workspace list --json | jq -r '.[] | select(.current) | .name'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := registry.Load()
			if err != nil {
				return err
			}
			active := registry.WorkspaceEntry{}
			if resolution, err := resolveWorkspace(); err == nil {
				active, _ = config.LookupPath(resolution.Project)
			}
			entries := make([]workspaceListEntry, 0, len(config.Workspaces))
			for _, entry := range config.Workspaces {
				entries = append(entries, workspaceListEntry{
					WorkspaceEntry: entry,
					Current:        active.Name != "" && entry.Name == active.Name,
					Default:        entry.Name == config.CurrentWorkspace,
				})
			}
			if flagJSON {
				return printJSON(entries)
			}
			if len(entries) == 0 {
				fmt.Println("No workspaces registered.")
				return nil
			}
			var out io.Writer = os.Stdout
			var table *tabwriter.Writer
			if stdoutIsTerminal() {
				table = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				out = table
			}
			fmt.Fprintln(out, "NAME\tPATH\tCURRENT")
			for _, entry := range entries {
				marker := ""
				if entry.Current {
					marker = "*"
				}
				fmt.Fprintf(out, "%s\t%s\t%s\n", entry.Name, entry.Path, marker)
			}
			if table != nil {
				return table.Flush()
			}
			return nil
		},
	}
}

func newWorkspaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "remove NAME",
		Short:             "Unregister a workspace without touching its task files",
		Example:           "  docket workspace remove client-b",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			removed, err := registry.Remove(args[0])
			if err != nil {
				return err
			}
			if !removed {
				return fmt.Errorf("workspace %q is not registered", args[0])
			}
			if flagJSON {
				return printJSON(map[string]string{"removed": args[0]})
			}
			fmt.Printf("Unregistered %s (task files untouched)\n", args[0])
			return nil
		},
	}
}

func newWorkspaceUseCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "use NAME | --clear",
		Short: "Set or clear the default workspace used outside any workspace",
		Long: `use saves a registered workspace (by name, or by the path of a registered
workspace) as the default for commands run outside any workspace directory.
Inside a workspace the nearest .docket/ still wins, and -w or
$DOCKET_WORKSPACE override both. The default is stored in the machine-local
registry (~/.config/docket/config.yaml). --clear removes it.`,
		Example: `  docket workspace use client-b
  docket workspace use ~/dev/client-b
  docket workspace use --clear`,
		Args: func(cmd *cobra.Command, args []string) error {
			if clear {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		ValidArgsFunction: completeWorkspaceArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear {
				previous, err := registry.ClearCurrent()
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]string{"cleared": previous})
				}
				if previous == "" {
					fmt.Println("No default workspace was set.")
					return nil
				}
				fmt.Printf("Cleared default workspace %s\n", previous)
				return nil
			}
			entry, err := registry.Use(args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(entry)
			}
			fmt.Printf("Default workspace is now %s (%s)\n", entry.Name, entry.Path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the saved default workspace")
	return cmd
}

func newWorkspaceCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show which workspace is in effect and why",
		Long: `Source is one of: flag (-w), env ($DOCKET_HOME or $DOCKET_WORKSPACE), cwd
(the nearest .docket/ above the current directory) or context (the default
saved by "docket workspace use"). Fails when no workspace is selected.`,
		Example: "  docket workspace current\n  docket -w client-b workspace current --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolution, err := resolveNamedWorkspace(flagWorkspace)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(resolution)
			}
			name := resolution.Name
			if name == "" {
				name = "(unregistered)"
			}
			fmt.Printf("Workspace: %s\n", name)
			fmt.Printf("Path: %s\n", resolution.Project)
			fmt.Printf("Source: %s\n", describeSource(resolution))
			return nil
		},
	}
}

func newWorkspacePathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path [NAME|PATH]",
		Short: "Print a workspace's project directory",
		Long: `path prints only the project directory (the one containing .docket/), so it
can be passed to cd. Without an argument it prints the workspace in effect.`,
		Example: `  cd (docket workspace path client-b)      # fish
  cd "$(docket workspace path client-b)"    # bash/zsh
  docket workspace path                     # the workspace in effect`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorkspaceArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := flagWorkspace
			if len(args) == 1 {
				selector = args[0]
			}
			resolution, err := resolveNamedWorkspace(selector)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"name": resolution.Name, "path": resolution.Project})
			}
			fmt.Println(resolution.Project)
			return nil
		},
	}
}

func describeSource(resolution registry.Resolution) string {
	switch resolution.Source {
	case registry.SourceFlag:
		return fmt.Sprintf("flag (-w %s)", resolution.Selector)
	case registry.SourceEnv:
		return fmt.Sprintf("env (%s=%s)", resolution.Variable, resolution.Selector)
	case registry.SourceCwd:
		return "cwd (nearest .docket/ above the current directory)"
	case registry.SourceContext:
		return "context (saved by `docket workspace use`)"
	}
	return string(resolution.Source)
}

// registerWorkspaceFlag adds the global -w/--workspace flag and its shell
// completion of registered workspace names.
func registerWorkspaceFlag(root *cobra.Command) {
	root.PersistentFlags().StringVarP(&flagWorkspace, "workspace", "w", "",
		"registered workspace `NAME` or PATH to use instead of the current directory's (see docket workspace --help)")
	_ = root.RegisterFlagCompletionFunc("workspace", completeWorkspaceNames)
}

// resolveWorkspace selects the workspace for this invocation from -w,
// $DOCKET_HOME/$DOCKET_WORKSPACE, the working directory and the saved
// context, in that order. Every command that opens a workspace goes through
// it, except init and workspace add, which act on a directory.
func resolveWorkspace() (registry.Resolution, error) {
	return registry.Resolve(flagWorkspace)
}

// selectedWorkspaceRoot returns the selected .docket directory without
// loading its config, for commands that must work while the config is broken.
func selectedWorkspaceRoot() (string, error) {
	resolution, err := resolveWorkspace()
	return resolution.Root, err
}

// resolveNamedWorkspace resolves selector (or the default order when empty)
// and fills in the registered name when the workspace was found by path.
func resolveNamedWorkspace(selector string) (registry.Resolution, error) {
	resolution, err := registry.Resolve(selector)
	if err != nil {
		return resolution, err
	}
	if resolution.Name == "" {
		if config, err := registry.Load(); err == nil {
			if entry, ok := config.LookupPath(resolution.Project); ok {
				resolution.Name = entry.Name
			}
		}
	}
	return resolution, nil
}

// completeWorkspaceNames completes registered workspace names, describing each
// with its path.
func completeWorkspaceNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	config, err := registry.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(config.Workspaces))
	for _, entry := range config.Workspaces {
		if strings.HasPrefix(entry.Name, toComplete) {
			names = append(names, entry.Name+"\t"+entry.Path)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeWorkspaceArg completes a single NAME positional argument.
func completeWorkspaceArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeWorkspaceNames(cmd, args, toComplete)
}
