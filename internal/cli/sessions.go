package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/session"
)

func newSessionCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "session",
		Short: "Optional task pointer for commands that omit TASK-ID",
		Long: `Session attachment is optional shorthand, not task assignment or locking.

Attaching stores a machine-local pointer from one session ID to one task. After
that, commands whose TASK-ID is optional can omit it. Use a stable, unique
--session value or DOCKET_SESSION when several agents or terminals share a
workspace. Without one, Docket uses the shared _global pointer.

Prefer explicit task IDs in automation unless omitting them materially improves
the harness integration.`,
		Example: `  export DOCKET_SESSION="agent-turn-42"
  docket session attach TASK-0007
  docket comment "Found the root cause"   # TASK-ID omitted
  docket move in-review                    # TASK-ID omitted
  docket session detach`,
	}
	command.AddCommand(newAttachCmd(), newDetachCmd(), newCurrentCmd())
	return command
}

func newAttachCmd() *cobra.Command {
	var comments int
	cmd := &cobra.Command{
		Use:   "attach TASK-ID",
		Short: "Point this session at a task and print its full context",
		Long: `Attach records a machine-local current-task pointer for the effective
session ID and prints the same context bundle as docket show. It does not claim,
assign, lock, or start the task. Explicit TASK-ID arguments always remain valid.`,
		Example: `  docket session attach TASK-0007
  docket session attach TASK-0007 --session agent-turn-42
  docket session attach TASK-0007 --comments 10`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			sid := sessionID()
			t, err := session.Attach(ws, args[0], sid, actor())
			if err != nil {
				return err
			}
			if err := appendEvent(ws, events.Event{
				Type: events.TaskAttached, Task: t.ID, Title: t.Title,
				Actor: actor(), Assignee: t.Assignee,
				Data: map[string]any{"session": sid},
			}); err != nil {
				return err
			}
			b, err := bundle.Build(ws, t.ID, comments)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(b)
			}
			fmt.Printf("Attached session %q to %s\n\n", sid, t.ID)
			printBundleHuman(b)
			return nil
		},
	}
	cmd.Flags().IntVar(&comments, "comments", 0, "limit context to the most recent N comments")
	return cmd
}

func newDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "detach",
		Short:   "Clear this session's optional current-task pointer",
		Example: "  docket session detach\n  docket session detach --session agent-turn-42",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			sid := sessionID()
			id, err := session.Detach(ws, sid, actor())
			if err != nil {
				return err
			}
			if id == "" {
				if flagJSON {
					return printJSON(map[string]any{"detached": nil})
				}
				fmt.Println("No task attached.")
				return nil
			}
			if err := appendEvent(ws, events.Event{
				Type: events.TaskDetached, Task: id, Actor: actor(),
				Data: map[string]any{"session": sid},
			}); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"detached": id})
			}
			fmt.Printf("Detached session %q from %s\n", sid, id)
			return nil
		},
	}
}

func newCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "current",
		Short:   "Print the task selected by this session pointer",
		Example: "  docket session current\n  docket session current --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			id := session.Current(ws, sessionID())
			if flagJSON {
				if id == "" {
					return printJSON(map[string]any{"current": nil})
				}
				return printJSON(map[string]string{"current": id})
			}
			if id == "" {
				fmt.Println("Not attached to any task.")
				return nil
			}
			fmt.Println(id)
			return nil
		},
	}
}
