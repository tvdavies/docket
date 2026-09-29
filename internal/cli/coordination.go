package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/events"
)

func newInboxCmd() *cobra.Command {
	var all, markRead, peek, reset bool
	var actorFlag string
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Show unread events addressed to an actor (poll-based coordination)",
		Long: `inbox returns events on tasks assigned to you that you have not yet seen,
tracked by a per-actor cursor. Use --all to ignore the assignee filter.

Notification readers acknowledge immediately:

    docket inbox --mark-read --json

--mark-read advances the cursor to the end of the batch it returned, before
you have processed it. It is read acknowledgement, not a transaction.

Durable consumers read, record, then acknowledge:

    docket inbox --peek --json          # {"events": [...], "checkpoint": "..."}
    # ...durably record the events, deduplicating repeats...
    docket inbox ack CHECKPOINT

--peek never moves the cursor, so a consumer that crashes before
acknowledging rereads the same events. Acknowledging a checkpoint twice is
harmless; a stale checkpoint, or one from another actor, workspace, or filter,
is rejected without moving the cursor. Use the same actor and --all setting to
read and acknowledge. If the event log was truncated or rewritten, reads fail
until you run "docket inbox --reset", which replays the current log.

Most event-driven automation should use configured handlers instead.`,
		Example: `  DOCKET_ACTOR=researcher docket inbox
  DOCKET_ACTOR=researcher docket inbox --mark-read --json
  docket inbox --actor sal --all --peek --json
  docket inbox ack --actor sal --all dkinbox1.eyJ2Ijox...`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			who := inboxActor(actorFlag)
			if reset {
				if err := events.ResetInbox(ws, who); err != nil {
					return err
				}
				if flagJSON {
					return printJSON(map[string]any{"actor": who, "position": 0})
				}
				fmt.Printf("Inbox cursor for %s reset; the next read replays the event log.\n", who)
				return nil
			}
			if peek {
				batch, err := events.PeekInbox(ws, who, all)
				if err != nil {
					return err
				}
				if flagJSON {
					return printJSON(batch)
				}
				if len(batch.Events) == 0 {
					fmt.Println("Inbox empty.")
				}
				for _, ev := range batch.Events {
					printEventLine(ev)
				}
				fmt.Printf("checkpoint: %s\n", batch.Checkpoint)
				return nil
			}
			evs, err := events.Inbox(ws, events.InboxOptions{Actor: who, All: all, MarkRead: markRead})
			if err != nil {
				return err
			}
			if flagJSON {
				if evs == nil {
					evs = []events.Event{}
				}
				return printJSON(evs)
			}
			if len(evs) == 0 {
				fmt.Println("Inbox empty.")
				return nil
			}
			for _, ev := range evs {
				printEventLine(ev)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&actorFlag, "actor", "", "actor whose inbox to read (defaults to current actor)")
	cmd.Flags().BoolVar(&all, "all", false, "ignore the assignee filter — every unread event")
	cmd.Flags().BoolVar(&markRead, "mark-read", false, "advance the cursor past the returned batch immediately")
	cmd.Flags().BoolVar(&peek, "peek", false, "return events and an acknowledgement checkpoint without moving the cursor")
	cmd.Flags().BoolVar(&reset, "reset", false, "recover after a history change by replaying the event log from the start")
	cmd.MarkFlagsMutuallyExclusive("peek", "mark-read", "reset")
	cmd.AddCommand(newInboxAckCmd())
	return cmd
}

func newInboxAckCmd() *cobra.Command {
	var all bool
	var actorFlag string
	cmd := &cobra.Command{
		Use:   "ack CHECKPOINT",
		Short: "Acknowledge a batch returned by inbox --peek",
		Long: `ack advances the actor's inbox cursor to a checkpoint from "docket inbox --peek".
Run it only after the batch has been durably recorded. Acknowledging a
checkpoint that is already covered succeeds without moving the cursor back.
Pass the same --actor and --all used for the read.`,
		Example: `  docket inbox ack --actor sal dkinbox1.eyJ2Ijox...`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			who := inboxActor(actorFlag)
			result, err := events.AckInbox(ws, who, all, args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(result)
			}
			if result.Applied {
				fmt.Printf("Acknowledged inbox for %s through position %d.\n", who, result.Position)
			} else {
				fmt.Printf("Already acknowledged; inbox for %s is at position %d.\n", who, result.Position)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&actorFlag, "actor", "", "actor whose inbox to acknowledge (defaults to current actor)")
	cmd.Flags().BoolVar(&all, "all", false, "acknowledge a checkpoint read with --all")
	return cmd
}

func inboxActor(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return actor()
}

func newEventsCmd() *cobra.Command {
	var since int
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Inspect the workspace's append-only event log",
		Long:  "--since N skips the first N physical event records; it is a cursor position, not an event sequence or timestamp.",
		Example: `  docket events
  docket events --since 20 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			evs, err := events.Since(ws, since)
			if err != nil {
				return err
			}
			if flagJSON {
				if evs == nil {
					evs = []events.Event{}
				}
				return printJSON(evs)
			}
			for _, ev := range evs {
				printEventLine(ev)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&since, "since", 0, "skip the first N events")
	return cmd
}

func newWatchCmd() *cobra.Command {
	var fromStart bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream events as they happen (push-based coordination)",
		Long: `watch blocks and emits each new event as a JSON line as soon as it is
appended, so a harness can react without polling. Output is always JSONL.
Most durable automation should use configured handlers instead.`,
		Example: `  docket watch
  docket watch --from-start | jq -c 'select(.type == "task.moved")'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			done := make(chan struct{})
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sig
				close(done)
			}()
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			return events.Watch(ws, fromStart, done, func(ev events.Event) error {
				return enc.Encode(ev)
			})
		},
	}
	cmd.Flags().BoolVar(&fromStart, "from-start", false, "replay existing events before streaming new ones")
	return cmd
}

func printEventLine(ev events.Event) {
	line := fmt.Sprintf("[%s] #%d %s", ev.Time, ev.Seq, ev.Type)
	if ev.Task != "" {
		line += " " + ev.Task
	}
	if ev.Actor != "" {
		line += " by " + ev.Actor
	}
	if len(ev.Data) > 0 {
		if b, err := json.Marshal(ev.Data); err == nil {
			line += " " + string(b)
		}
	}
	fmt.Println(line)
}
