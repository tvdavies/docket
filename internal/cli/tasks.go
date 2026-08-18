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
		Example: `  mkdir my-workspace && cd my-workspace
  docket init
  docket init  # safe: already initialised and registered`,
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
		Use:   "new --title TITLE",
		Short: "Create a task and print its new ID",
		Example: `  docket new --title "Fix login cache"
  docket new --title "Fix login cache" --label bug --status ready
  docket new --title "Investigate" --desc-file ./brief.md --project PROJ-0001
  ID=$(docket new --title "Machine-readable creation")`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			description, err := readDescription(desc, descFile)
			if err != nil {
				return err
			}
			operations := actions.Tasks{
				Workspace: ws, Actor: actor(), Session: sessionID(),
				Append: func(event events.Event) error { return appendEvent(ws, event) },
			}
			t, err := operations.Create(task.CreateOptions{
				Title:       title,
				Description: description,
				Project:     project,
				Labels:      labels,
				Status:      status,
			})
			if err != nil {
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
	cmd.MarkFlagsMutuallyExclusive("desc", "desc-file")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newListCmd() *cobra.Command {
	var status, label, project, assignee string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks, with optional exact filters",
		Example: `  docket list
  docket list --status in-review
  docket list --label bug --assignee researcher
  docket list --project PROJ-0001 --json`,
		Args: cobra.NoArgs,
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
	var comments int
	cmd := &cobra.Command{
		Use:   "show [TASK-ID]",
		Short: "Show the complete context needed to understand or resume a task",
		Long: `Show prints the task description, comments, attachments, project, and
resolved relationships. TASK-ID may be omitted only when an optional session
pointer is attached; explicit IDs are recommended for scripts and agents.`,
		Example: `  docket show TASK-0007
  docket show TASK-0007 --comments 10
  docket show TASK-0007 --json`,
		Args: cobra.MaximumNArgs(1),
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
	cmd.Flags().IntVar(&comments, "comments", 0, "show only the most recent N comments (0 means all)")
	return cmd
}

func newContextCmd() *cobra.Command {
	var comments int
	cmd := &cobra.Command{
		Use:   "context [TASK-ID]",
		Short: "Compatibility alias for show --comments",
		Long:  "This compatibility command remains for existing scripts. New usage should call `docket show [TASK-ID] --comments N`.",
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
		Short: "Change a task's title, description, or assignee",
		Long: `At least one edit flag should be supplied. TASK-ID may be omitted only
when an optional session pointer is attached. Use --assignee "" to clear it.`,
		Example: `  docket edit TASK-0007 --title "Clarify cache invalidation"
  docket edit TASK-0007 --desc-file ./updated-brief.md
  docket edit TASK-0007 --assignee researcher
  docket edit TASK-0007 --assignee ""`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("title") && !cmd.Flags().Changed("desc") && !cmd.Flags().Changed("desc-file") && !cmd.Flags().Changed("assignee") {
				return fmt.Errorf("no changes requested; supply --title, --desc, --desc-file, or --assignee")
			}
			if cmd.Flags().Changed("title") && strings.TrimSpace(title) == "" {
				return fmt.Errorf("title cannot be empty")
			}
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
			var titleChange, descriptionChange, assigneeChange *string
			if cmd.Flags().Changed("title") {
				titleChange = &title
			}
			if descChanged {
				description = strings.TrimRight(description, "\n")
				descriptionChange = &description
			}
			if cmd.Flags().Changed("assignee") {
				assigneeChange = &assignee
			}
			operations := actions.Tasks{
				Workspace: ws, Actor: actor(), Session: sessionID(),
				Append: func(event events.Event) error { return appendEvent(ws, event) },
			}
			t, err := operations.Edit(id, actions.EditOptions{
				Title: titleChange, Description: descriptionChange, Assignee: assigneeChange,
			})
			if err != nil {
				return err
			}
			return reportTask(t, "updated")
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&desc, "desc", "", "new description")
	cmd.Flags().StringVar(&descFile, "desc-file", "", "read new description from file ('-' for stdin)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "set assignee (empty clears)")
	cmd.MarkFlagsMutuallyExclusive("desc", "desc-file")
	return cmd
}

func newMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move [TASK-ID] STATUS",
		Short: "Move a task to a configured status lane",
		Long: `With two arguments, the first is TASK-ID and the second is STATUS. With
one argument, it is STATUS for the optional task attached to this session.
Explicit IDs are recommended for scripts and agents.`,
		Example: `  docket move TASK-0007 in-progress
  docket move TASK-0007 done

  # Only after: docket session attach TASK-0007
  docket move in-review`,
		Args: cobra.RangeArgs(1, 2),
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
		Use:   "label [TASK-ID]",
		Short: "Add or remove task labels",
		Long: `Repeat --add or --remove to change several labels. TASK-ID may be omitted
only when an optional session pointer is attached.`,
		Example: `  docket label TASK-0007 --add bug
  docket label TASK-0007 --add urgent --add client-visible
  docket label TASK-0007 --remove bug`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(add) == 0 && len(remove) == 0 {
				return fmt.Errorf("no label changes requested; supply --add LABEL or --remove LABEL")
			}
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
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	Project  string     `json:"project,omitempty"`
	Labels   []string   `json:"labels"`
	Assignee string     `json:"assignee,omitempty"`
	Wait     *task.Wait `json:"wait,omitempty"`
	Updated  string     `json:"updated_at"`
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
			Labels: labels, Assignee: t.Assignee, Wait: t.Wait,
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
	fmt.Fprintln(w, "ID\tSTATUS\tTITLE\tLABELS\tWAITING")
	for _, t := range tasks {
		waiting := ""
		if t.Wait != nil {
			waiting = t.Wait.Kind
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, truncate(t.Title, 50), strings.Join(t.Labels, ","), waiting)
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
