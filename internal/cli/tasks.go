package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Ensure this directory is a registered Docket workspace",
		Long: `init is idempotent: it creates .docket/ when absent and registers the
workspace with the machine-wide service when unregistered. Re-running it is a
successful no-op; it never creates duplicate registrations.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			ws, err := workspace.Init(cwd)
			if err != nil {
				return err
			}
			entry, err := registry.Add(cwd, "")
			if err != nil {
				return fmt.Errorf("workspace is ready at %s, but service registration failed: %w", ws.Root, err)
			}
			if flagJSON {
				return printJSON(map[string]any{"workspace": ws.Root, "registration": entry})
			}
			fmt.Printf("Docket workspace ready at %s\n", ws.Root)
			fmt.Printf("Registered with the service as %s\n", entry.Name)
			return nil
		},
	}
}

func newNewCmd() *cobra.Command {
	var title, desc, descFile, project, status string
	var labels []string
	cmd := &cobra.Command{
		Use:   "new --title T [flags]",
		Short: "Create a new task",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			description, err := readDescription(desc, descFile)
			if err != nil {
				return err
			}
			t, err := task.Create(ws, task.CreateOptions{
				Title:       title,
				Description: description,
				Project:     project,
				Labels:      labels,
				Assignee:    "",
				Status:      status,
			})
			if err != nil {
				return err
			}
			if err := appendEvent(ws, events.Event{
				Type: events.TaskCreated, Task: t.ID, Title: t.Title,
				Actor: actor(), Assignee: t.Assignee,
			}); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"id": t.ID})
			}
			fmt.Println(t.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&desc, "desc", "", "task description")
	cmd.Flags().StringVar(&descFile, "desc-file", "", "read description from file ('-' for stdin)")
	cmd.Flags().StringVar(&project, "project", "", "project id")
	cmd.Flags().StringVar(&status, "status", "", "initial status (defaults to first lane)")
	cmd.Flags().StringSliceVar(&labels, "label", nil, "label (repeatable)")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newListCmd() *cobra.Command {
	var status, label, project, assignee string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks, optionally filtered",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			tasks, err := task.All(ws)
			if err != nil {
				return err
			}
			var filtered []*task.Task
			for _, t := range tasks {
				if status != "" && t.Status != status {
					continue
				}
				if label != "" && !t.HasLabel(label) {
					continue
				}
				if project != "" && t.Project != project {
					continue
				}
				if assignee != "" && t.Assignee != assignee {
					continue
				}
				filtered = append(filtered, t)
			}
			if flagJSON {
				return printJSON(taskSummaries(filtered))
			}
			printTaskTable(filtered)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status")
	cmd.Flags().StringVar(&label, "label", "", "filter by label")
	cmd.Flags().StringVar(&project, "project", "", "filter by project")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee")
	return cmd
}

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [TASK-ID]",
		Short: "Show a task's full dossier",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(ws, firstArg(args))
			if err != nil {
				return err
			}
			b, err := bundle.Build(ws, id, 0)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(b)
			}
			printBundleHuman(b)
			return nil
		},
	}
}

func newContextCmd() *cobra.Command {
	var comments int
	cmd := &cobra.Command{
		Use:   "context [TASK-ID]",
		Short: "Print the context handoff bundle a fresh session reads to resume",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(ws, firstArg(args))
			if err != nil {
				return err
			}
			b, err := bundle.Build(ws, id, comments)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(b)
			}
			printBundleHuman(b)
			return nil
		},
	}
	cmd.Flags().IntVar(&comments, "comments", 0, "limit to the most recent N comments (0 = all)")
	return cmd
}

func newEditCmd() *cobra.Command {
	var title, desc, descFile, assignee string
	cmd := &cobra.Command{
		Use:   "edit [TASK-ID]",
		Short: "Edit a task's title, description, or assignee",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(ws, firstArg(args))
			if err != nil {
				return err
			}
			descChanged := cmd.Flags().Changed("desc") || cmd.Flags().Changed("desc-file")
			var description string
			if descChanged {
				description, err = readDescription(desc, descFile)
				if err != nil {
					return err
				}
			}
			assigneeChanged := cmd.Flags().Changed("assignee")
			t, err := task.Update(ws, id, func(t *task.Task) error {
				if title != "" {
					t.Title = title
				}
				if descChanged {
					t.Description = strings.TrimRight(description, "\n")
				}
				if assigneeChanged {
					t.Assignee = assignee
				}
				return nil
			})
			if err != nil {
				return err
			}
			if assigneeChanged {
				if err := appendEvent(ws, events.Event{
					Type: events.TaskAssigned, Task: t.ID, Title: t.Title,
					Actor: actor(), Assignee: t.Assignee,
				}); err != nil {
					return err
				}
			} else {
				if err := appendEvent(ws, events.Event{
					Type: events.TaskUpdated, Task: t.ID, Title: t.Title,
					Actor: actor(), Assignee: t.Assignee,
				}); err != nil {
					return err
				}
			}
			return reportTask(t, "updated")
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&desc, "desc", "", "new description")
	cmd.Flags().StringVar(&descFile, "desc-file", "", "read new description from file ('-' for stdin)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "set assignee (empty clears)")
	return cmd
}

func newMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move [TASK-ID] STATUS",
		Short: "Change a task's status (lane)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			var explicit, status string
			if len(args) == 2 {
				explicit, status = args[0], args[1]
			} else {
				status = args[0]
			}
			id, err := resolveTaskID(ws, explicit)
			if err != nil {
				return err
			}
			operations := actions.Tasks{
				Workspace: ws, Actor: actor(), Session: sessionID(),
				Append: func(event events.Event) error { return appendEvent(ws, event) },
			}
			t, from, err := operations.Move(id, status)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"id": t.ID, "from": from, "to": status})
			}
			fmt.Printf("%s: %s → %s\n", t.ID, from, status)
			return nil
		},
	}
}

func newLabelCmd() *cobra.Command {
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "label [TASK-ID] [--add L] [--remove L]",
		Short: "Add or remove labels on a task",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(ws, firstArg(args))
			if err != nil {
				return err
			}
			operations := actions.Tasks{
				Workspace: ws, Actor: actor(), Session: sessionID(),
				Append: func(event events.Event) error { return appendEvent(ws, event) },
			}
			t, err := operations.Label(id, add, remove)
			if err != nil {
				return err
			}
			return reportTask(t, "labeled")
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "label to add (repeatable)")
	cmd.Flags().StringSliceVar(&remove, "remove", nil, "label to remove (repeatable)")
	return cmd
}

// --- helpers shared by task commands ---

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func readDescription(desc, descFile string) (string, error) {
	if descFile == "-" {
		data, err := readStdin()
		return string(data), err
	}
	if descFile != "" {
		data, err := os.ReadFile(descFile)
		return string(data), err
	}
	return desc, nil
}

type taskSummary struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Project  string   `json:"project,omitempty"`
	Labels   []string `json:"labels"`
	Assignee string   `json:"assignee,omitempty"`
	Updated  string   `json:"updated_at"`
}

func taskSummaries(tasks []*task.Task) []taskSummary {
	out := make([]taskSummary, 0, len(tasks))
	for _, t := range tasks {
		labels := t.Labels
		if labels == nil {
			labels = []string{}
		}
		out = append(out, taskSummary{
			ID: t.ID, Title: t.Title, Status: t.Status, Project: t.Project,
			Labels: labels, Assignee: t.Assignee,
			Updated: t.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out
}

func printTaskTable(tasks []*task.Task) {
	if len(tasks) == 0 {
		fmt.Println("No tasks.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tTITLE\tLABELS")
	for _, t := range tasks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Status, truncate(t.Title, 50), strings.Join(t.Labels, ","))
	}
	_ = w.Flush()
}

func reportTask(t *task.Task, verb string) error {
	if flagJSON {
		return printJSON(taskSummaries([]*task.Task{t})[0])
	}
	fmt.Printf("%s %s\n", t.ID, verb)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
