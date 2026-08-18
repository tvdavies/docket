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
	var all, markRead bool
	var actorFlag string
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Show unread events addressed to an actor (poll-based coordination)",
		Long: `inbox returns events on tasks assigned to you that you have not yet seen,
tracked by a per-actor cursor. A polling consumer runs:

    docket inbox --mark-read --json

to drain its queue. Use --all to ignore the assignee filter. Most event-driven
automation should use configured handlers instead.`,
		Example: `  DOCKET_ACTOR=researcher docket inbox
  DOCKET_ACTOR=researcher docket inbox --mark-read --json
  docket inbox --actor researcher --all --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			who := actorFlag
			if who == "" {
				who = actor()
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
	cmd.Flags().BoolVar(&markRead, "mark-read", false, "advance the cursor past everything read")
	return cmd
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
