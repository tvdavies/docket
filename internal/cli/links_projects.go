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

// relationshipFlags binds one --<kind> TARGET flag per configured relationship.
func linkRunner(eventType string, link bool) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ws, err := openWS()
		if err != nil {
			return err
		}
		from := args[0]
		// Find which relationship flag was set.
		var kind, to string
		for _, rel := range ws.Config.RelNames() {
			if v, _ := cmd.Flags().GetString(rel); v != "" {
				if kind != "" {
					return fmt.Errorf("specify exactly one relationship flag")
				}
				kind, to = rel, v
			}
		}
		if kind == "" {
			return fmt.Errorf("specify a relationship flag, e.g. --blocks TASK-0010 (configured: %s)", strings.Join(ws.Config.RelNames(), ", "))
		}
		if link {
			err = task.Link(ws, from, kind, to)
		} else {
			err = task.Unlink(ws, from, kind, to)
		}
		if err != nil {
			return err
		}
		if err := appendEvent(ws, events.Event{
			Type: eventType, Task: from, Actor: actor(),
			Data: map[string]any{"kind": kind, "to": to},
		}); err != nil {
			return err
		}
		if flagJSON {
			return printJSON(map[string]string{"from": from, "kind": kind, "to": to})
		}
		op := "linked"
		if !link {
			op = "unlinked"
		}
		fmt.Printf("%s %s %s %s\n", from, op, kind, to)
		return nil
	}
}

func addRelFlags(cmd *cobra.Command) {
	// Relationship flags are resolved at runtime from config; declare the
	// built-in defaults so --help is useful even before a workspace exists.
	for _, name := range []string{"blocks", "blocked-by", "parent", "subtasks", "relates", "duplicate-of", "duplicates"} {
		cmd.Flags().String(name, "", "target task id for the "+name+" relationship")
	}
}

func newLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link TASK-ID --RELATIONSHIP TARGET",
		Short: "Create a typed task relationship and its inverse",
		Long: `Supply exactly one relationship flag. Docket updates both tasks, so
"TASK-0007 --blocks TASK-0010" also records TASK-0007 under TASK-0010's
"blocked-by" relationship. Available flags come from workspace configuration.`,
		Example: `  docket link TASK-0007 --blocks TASK-0010
  docket link TASK-0007 --parent TASK-0001
  docket link TASK-0007 --relates TASK-0008`,
		Args: cobra.ExactArgs(1),
		RunE: linkRunner(events.TaskLinked, true),
	}
	addRelFlags(cmd)
	return cmd
}

func newUnlinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unlink TASK-ID --RELATIONSHIP TARGET",
		Short: "Remove a typed task relationship and its inverse",
		Example: `  docket unlink TASK-0007 --blocks TASK-0010
  docket unlink TASK-0007 --relates TASK-0008`,
		Args: cobra.ExactArgs(1),
		RunE: linkRunner(events.TaskUnlinked, false),
	}
	addRelFlags(cmd)
	return cmd
}

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Create and inspect named task groupings",
		Long:  "Projects are lightweight groupings inside one Docket workspace; they are not separate workspaces.",
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
		Use:     "show PROJ-ID",
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
