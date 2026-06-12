package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tvdavies/tadu/internal/bundle"
	"github.com/tvdavies/tadu/internal/events"
	"github.com/tvdavies/tadu/internal/session"
)

func newAttachCmd() *cobra.Command {
	var comments int
	cmd := &cobra.Command{
		Use:   "attach TASK-ID",
		Short: "Bind this session to a task and print its context bundle (the handoff)",
		Args:  cobra.ExactArgs(1),
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
			_ = events.Append(ws, events.Event{
				Type: events.TaskAttached, Task: t.ID, Title: t.Title,
				Actor: actor(), Assignee: t.Assignee,
				Data: map[string]any{"session": sid},
			})
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
		Use:   "detach",
		Short: "Unbind this session from its current task",
		Args:  cobra.NoArgs,
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
			_ = events.Append(ws, events.Event{
				Type: events.TaskDetached, Task: id, Actor: actor(),
				Data: map[string]any{"session": sid},
			})
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
		Use:   "current",
		Short: "Show the task this session is attached to",
		Args:  cobra.NoArgs,
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
