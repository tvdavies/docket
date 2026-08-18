package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

func operationsFor(ws *workspace.Workspace) actions.Tasks {
	return actions.Tasks{
		Workspace: ws, Actor: actor(), Session: sessionID(),
		Append: func(event events.Event) error { return appendEvent(ws, event) },
	}
}

func newWaitCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "wait",
		Short: "Record and resolve an external condition blocking a task",
		Long: `A task has at most one active wait. Workflow status remains unchanged while
waiting. Resolving the exact wait ID emits task.resumed so automation can wake
the assignee for the task's current stage.`,
	}
	command.AddCommand(newWaitSetCmd(), newWaitResolveCmd(), newWaitShowCmd())
	return command
}

func newWaitSetCmd() *cobra.Command {
	var kind, reason, reference string
	command := &cobra.Command{
		Use:   "set TASK-ID",
		Short: "Mark a task as waiting",
		Example: `  docket wait set JOB-0001 --kind plan_feedback \
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
		Example: `  docket wait resolve JOB-0001 --wait-id wait-abc123 --result approved`,
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

func newWaitShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show TASK-ID",
		Short: "Show the task's active wait",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			value, err := task.Load(ws, args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(value.Wait)
			}
			if value.Wait == nil {
				fmt.Printf("%s is not waiting.\n", value.ID)
				return nil
			}
			fmt.Printf("%s  %s  %s\n", value.Wait.ID, value.Wait.Kind, value.Wait.Reason)
			if value.Wait.Reference != "" {
				fmt.Println(value.Wait.Reference)
			}
			return nil
		},
	}
}

func newReferenceCmd() *cobra.Command {
	command := &cobra.Command{
		Use:     "reference",
		Aliases: []string{"ref"},
		Short:   "Manage durable external links on a task",
	}
	command.AddCommand(newReferenceAddCmd(), newReferenceRemoveCmd(), newReferenceListCmd())
	return command
}

func newReferenceAddCmd() *cobra.Command {
	var kind, referenceURL, title string
	command := &cobra.Command{
		Use:     "add TASK-ID",
		Short:   "Add a typed external link",
		Example: `  docket reference add JOB-0001 --kind plan --url https://example.com/plan --title "Plan v1"`,
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
		Use:   "remove TASK-ID REFERENCE-ID",
		Short: "Remove a reference by stable ID",
		Args:  cobra.ExactArgs(2),
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

func newReferenceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list TASK-ID",
		Short: "List task references",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			value, err := task.Load(ws, args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(value.References)
			}
			if len(value.References) == 0 {
				fmt.Println("No references.")
				return nil
			}
			for _, reference := range value.References {
				fmt.Printf("%s  %s  %s", reference.ID, reference.Kind, reference.URL)
				if reference.Title != "" {
					fmt.Printf("  %s", reference.Title)
				}
				fmt.Println()
			}
			return nil
		},
	}
}
