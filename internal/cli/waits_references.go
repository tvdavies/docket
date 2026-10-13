package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/workspace"
)

// operationsFor records task events under the task lock and drains inline
// handlers only after the lock is released, so a handler may mutate the same
// task without deadlocking.
func operationsFor(ws *workspace.Workspace) actions.Tasks {
	return actions.Tasks{
		Workspace: ws, Actor: actor(), Session: sessionID(),
		Append:    func(event events.Event) error { return recordEvent(ws, event) },
		Committed: func() { drainInline(ws) },
	}
}

func newWaitCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "wait",
		Short: "Mark a task as waiting on something external, or resolve it",
		Long: `A task has at most one active wait. Its status stays unchanged while it
waits. Resolving requires the exact wait ID, so a stale watcher cannot clear a
newer condition, and emits task.resumed so automation can wake the assignee.

"docket show" prints the active wait and its ID.`,
		Example: `  docket wait set TASK-0007 --kind review --reason "Awaiting security review"
  docket show TASK-0007 --view current --json | jq -r .wait.id
  docket wait resolve TASK-0007 --wait-id wait-abc123 --result approved`,
	}
	command.AddCommand(newWaitSetCmd(), newWaitResolveCmd())
	return command
}

func newWaitSetCmd() *cobra.Command {
	var kind, reason, reference string
	command := &cobra.Command{
		Use:   "set TASK-ID",
		Short: "Mark a task as waiting and print the wait ID",
		Long:  "--kind is a machine-readable category for automation; --reason is for people.",
		Example: `  docket wait set TASK-0007 --kind plan_feedback \
    --reason "Awaiting review of plan v1" --ref https://example.com/plan`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			value, err := operationsFor(ws).SetWait(args[0], actions.SetWaitOptions{
				Kind: kind, Reason: reason, Reference: reference,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(value.Wait)
			}
			fmt.Printf("%s waiting: %s (%s)\n", value.ID, value.Wait.Reason, value.Wait.ID)
			return nil
		},
	}
	command.Flags().StringVar(&kind, "kind", "", "machine-readable wait kind")
	command.Flags().StringVar(&reason, "reason", "", "human-readable reason")
	command.Flags().StringVar(&reference, "ref", "", "optional related URL")
	_ = command.MarkFlagRequired("kind")
	_ = command.MarkFlagRequired("reason")
	return command
}

func newWaitResolveCmd() *cobra.Command {
	var waitID, result string
	command := &cobra.Command{
		Use:     "resolve TASK-ID",
		Short:   "Clear an exact active wait and resume automation",
		Example: `  docket wait resolve TASK-0007 --wait-id wait-abc123 --result approved`,
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			value, err := operationsFor(ws).ResolveWait(args[0], actions.ResolveWaitOptions{
				WaitID: waitID, Result: result,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"id": value.ID, "wait_id": waitID, "result": result})
			}
			fmt.Printf("%s resumed (resolved %s", value.ID, waitID)
			if result != "" {
				fmt.Printf(": %s", result)
			}
			fmt.Println(")")
			return nil
		},
	}
	command.Flags().StringVar(&waitID, "wait-id", "", "exact active wait ID")
	command.Flags().StringVar(&result, "result", "", "optional resolution such as approved or changed")
	_ = command.MarkFlagRequired("wait-id")
	return command
}

func newReferenceCmd() *cobra.Command {
	command := &cobra.Command{
		Use:     "reference",
		Aliases: []string{"ref"},
		Short:   "Add or remove typed external links on a task",
		Long: `References are durable, typed links such as pull requests, plans, or
tickets. Each gets a stable ID; "docket show" lists them with their IDs.`,
		Example: `  docket reference add TASK-0007 --kind pr --url https://github.com/org/repo/pull/42
  docket reference remove TASK-0007 ref-abc123`,
	}
	command.AddCommand(newReferenceAddCmd(), newReferenceRemoveCmd())
	return command
}

func newReferenceAddCmd() *cobra.Command {
	var kind, referenceURL, title string
	command := &cobra.Command{
		Use:     "add TASK-ID",
		Short:   "Add a typed external link",
		Example: `  docket reference add TASK-0007 --kind plan --url https://example.com/plan --title "Plan v1"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			_, reference, err := operationsFor(ws).AddReference(args[0], kind, referenceURL, title)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(reference)
			}
			fmt.Printf("%s added %s reference %s\n", args[0], reference.Kind, reference.ID)
			return nil
		},
	}
	command.Flags().StringVar(&kind, "kind", "", "machine-readable reference kind")
	command.Flags().StringVar(&referenceURL, "url", "", "absolute reference URL")
	command.Flags().StringVar(&title, "title", "", "optional display title")
	_ = command.MarkFlagRequired("kind")
	_ = command.MarkFlagRequired("url")
	return command
}

func newReferenceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove TASK-ID REFERENCE-ID",
		Short:   "Remove a reference by its ID",
		Example: "  docket reference remove TASK-0007 ref-abc123",
		Args:    cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			_, reference, err := operationsFor(ws).RemoveReference(args[0], args[1])
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(reference)
			}
			fmt.Printf("%s removed %s reference %s\n", args[0], reference.Kind, reference.ID)
			return nil
		},
	}
}
