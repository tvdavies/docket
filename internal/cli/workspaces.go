package cli

import (
	"fmt"
	"os"
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
are mainly for inspection, validation, manual registration, and removal from
the machine-wide service registry. Removing a registration never removes task
files.`,
	}
	cmd.AddCommand(newWorkspaceCheckCmd(), newWorkspaceAddCmd(), newWorkspaceListCmd(), newWorkspaceRemoveCmd())
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
		Long:    "PATH may be the project root, .docket directory, or any descendant. The command fails with the exact config parse or validation error.",
		Example: "  docket workspace check\n  docket workspace check ~/dev/client-b\n  docket workspace check --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			ws, err := workspace.OpenAt(path)
			if err != nil {
				return err
			}
			tasks, err := task.All(ws)
			if err != nil {
				return err
			}
			summary := map[string]any{
				"workspace": ws.Root,
				"statuses":  ws.Config.Statuses,
				"tasks":     len(tasks),
				"events":    events.Count(ws),
				"handlers":  len(ws.Config.Handlers),
			}
			if flagJSON {
				return printJSON(summary)
			}
			fmt.Printf("Workspace valid: %s\n", ws.Root)
			fmt.Printf("Statuses: %v\n", ws.Config.Statuses)
			fmt.Printf("Tasks: %d  Events: %d  Handlers: %d\n", len(tasks), events.Count(ws), len(ws.Config.Handlers))
			return nil
		},
	}
}

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List workspaces registered with the service",
		Example: "  docket workspace list\n  docket workspace list --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := registry.Load()
			if err != nil {
				return err
			}
			if flagJSON {
				workspaces := config.Workspaces
				if workspaces == nil {
					workspaces = []registry.WorkspaceEntry{}
				}
				return printJSON(workspaces)
			}
			if len(config.Workspaces) == 0 {
				fmt.Println("No workspaces registered.")
				return nil
			}
			writer := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(writer, "NAME\tPATH")
			for _, entry := range config.Workspaces {
				fmt.Fprintf(writer, "%s\t%s\n", entry.Name, entry.Path)
			}
			return writer.Flush()
		},
	}
}

func newWorkspaceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove NAME",
		Short:   "Unregister a workspace without touching its task files",
		Example: "  docket workspace remove client-b",
		Args:    cobra.ExactArgs(1),
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
