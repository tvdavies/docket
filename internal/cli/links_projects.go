package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/project"
	"github.com/tvdavies/docket/internal/task"
)

func newLinkCmd() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "link TASK-ID --RELATIONSHIP TARGET-ID",
		Short: "Add or remove a typed relationship between two tasks",
		Long: `Supply exactly one relationship flag. Docket updates both tasks, so
"TASK-0007 --blocks TASK-0010" also records TASK-0007 under TASK-0010's
"blocked-by" relationship. --remove deletes the relationship and its inverse.

Relationship flags come from the workspace configuration; the defaults are
listed below. "docket show" prints a task's resolved relationships.`,
		Example: `  docket link TASK-0007 --blocks TASK-0010
  docket link TASK-0007 --parent TASK-0001
  docket link TASK-0007 --blocks TASK-0010 --remove`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			from := args[0]
			var kind, to string
			for _, rel := range ws.Config.RelNames() {
				if value, _ := cmd.Flags().GetString(rel); value != "" {
					if kind != "" {
						return fmt.Errorf("specify exactly one relationship flag")
					}
					kind, to = rel, value
				}
			}
			if kind == "" {
				return fmt.Errorf("specify a relationship flag, e.g. --blocks TASK-0010 (configured: %s)", strings.Join(ws.Config.RelNames(), ", "))
			}
			operations := operationsFor(ws)
			verb := "linked"
			if remove {
				verb = "unlinked"
				err = operations.Unlink(from, kind, to)
			} else {
				err = operations.Link(from, kind, to)
			}
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"from": from, "kind": kind, "to": to, "removed": remove})
			}
			fmt.Printf("%s %s %s %s\n", from, verb, kind, to)
			return nil
		},
	}
	// Relationship flags are resolved at runtime from config; declare the
	// built-in defaults so --help is useful before a workspace exists.
	for _, name := range []string{"blocks", "blocked-by", "parent", "subtasks", "relates", "duplicate-of", "duplicates"} {
		cmd.Flags().String(name, "", "TARGET-ID for the "+name+" relationship")
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the relationship and its inverse instead of adding it")
	return cmd
}

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Group tasks into named projects",
		Long:  "Projects are lightweight groupings inside one workspace, not separate workspaces. Assign a task with \"docket new --project PROJECT-ID\".",
		Example: `  PROJECT=$(docket project new --name "Website")
  docket new --title "Improve navigation" --project "$PROJECT"
  docket project show "$PROJECT"`,
	}
	cmd.AddCommand(newProjectNewCmd(), newProjectListCmd(), newProjectShowCmd())
	return cmd
}

func newProjectNewCmd() *cobra.Command {
	var name, desc string
	cmd := &cobra.Command{
		Use:     "new --name NAME",
		Short:   "Create a project and print its ID",
		Example: "  docket project new --name \"Website\"\n  docket project new --name \"Website\" --desc \"Public site work\"",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			p, err := project.Create(ws, name, desc)
			if err != nil {
				return err
			}
			if err := appendEvent(ws, events.Event{
				Type: events.ProjectCreated, Title: p.Name, Actor: actor(),
				Data: map[string]any{"project": p.ID},
			}); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"id": p.ID, "name": p.Name})
			}
			fmt.Println(p.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name (required)")
	cmd.Flags().StringVar(&desc, "desc", "", "project description")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newProjectListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List projects",
		Example: "  docket project list\n  docket project list --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			projects, err := project.All(ws)
			if err != nil {
				return err
			}
			if flagJSON {
				type pv struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				out := []pv{}
				for _, p := range projects {
					out = append(out, pv{p.ID, p.Name})
				}
				return printJSON(out)
			}
			if len(projects) == 0 {
				fmt.Println("No projects.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME")
			for _, p := range projects {
				fmt.Fprintf(w, "%s\t%s\n", p.ID, p.Name)
			}
			return w.Flush()
		},
	}
}

func newProjectShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show PROJECT-ID",
		Short:   "Show a project and its member tasks",
		Example: "  docket project show PROJ-0001\n  docket project show PROJ-0001 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			p, err := project.Load(ws, args[0])
			if err != nil {
				return err
			}
			tasks, _ := task.All(ws)
			var members []*task.Task
			for _, t := range tasks {
				if t.Project == p.ID {
					members = append(members, t)
				}
			}
			if flagJSON {
				return printJSON(map[string]any{
					"id": p.ID, "name": p.Name, "description": p.Description,
					"tasks": taskSummaries(members),
				})
			}
			fmt.Printf("# %s — %s\n", p.ID, p.Name)
			if p.Description != "" {
				fmt.Printf("\n%s\n", p.Description)
			}
			fmt.Printf("\n## Tasks (%d)\n", len(members))
			printTaskTable(members)
			return nil
		},
	}
}
