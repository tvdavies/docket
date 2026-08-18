package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/registry"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspaces watched by the machine-wide Docket service",
	}
	cmd.AddCommand(newWorkspaceAddCmd(), newWorkspaceListCmd(), newWorkspaceRemoveCmd())
	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "add [PATH]",
		Short: "Register a workspace with the machine-wide service",
		Args:  cobra.MaximumNArgs(1),
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

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List workspaces registered with the machine-wide service",
		Args:  cobra.NoArgs,
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
		Use:   "remove NAME",
		Short: "Unregister a workspace (task files are untouched)",
		Args:  cobra.ExactArgs(1),
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
