package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tvdavies/tadu/internal/events"
	"github.com/tvdavies/tadu/internal/store"
	"github.com/tvdavies/tadu/internal/task"
	"github.com/tvdavies/tadu/internal/workspace"
)

func newCommentCmd() *cobra.Command {
	var fromFile string
	cmd := &cobra.Command{
		Use:   "comment [TASK-ID] [text]",
		Short: "Append a comment to a task (append-only)",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			// Disambiguate (id, text) vs (text) using whether arg0 looks like an id.
			explicit, text := splitIDAndText(ws, args)
			if fromFile != "" {
				var data []byte
				if fromFile == "-" {
					data, err = readStdin()
				} else {
					data, err = os.ReadFile(fromFile)
				}
				if err != nil {
					return err
				}
				text = string(data)
			}
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("comment text is required (positional, or --file)")
			}
			id, err := resolveTaskID(ws, explicit)
			if err != nil {
				return err
			}
			t, err := task.Load(ws, id)
			if err != nil {
				return err
			}
			c, err := task.AddComment(ws, id, actor(), sessionID(), text)
			if err != nil {
				return err
			}
			_ = events.Append(ws, events.Event{
				Type: events.TaskCommented, Task: t.ID, Title: t.Title,
				Actor: actor(), Assignee: t.Assignee,
			})
			if flagJSON {
				return printJSON(map[string]string{"task": t.ID, "comment": c.File})
			}
			fmt.Printf("%s commented (%s)\n", t.ID, c.File)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromFile, "file", "", "read comment body from file ('-' for stdin)")
	return cmd
}

func newAttachFileCmd() *cobra.Command {
	var caption string
	cmd := &cobra.Command{
		Use:   "attach-file [TASK-ID] PATH",
		Short: "Attach a file (any media) to a task",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			var explicit, path string
			if len(args) == 2 {
				explicit, path = args[0], args[1]
			} else {
				path = args[0]
			}
			id, err := resolveTaskID(ws, explicit)
			if err != nil {
				return err
			}
			t, err := task.Load(ws, id)
			if err != nil {
				return err
			}
			att, err := task.AttachFile(ws, id, path, caption, actor())
			if err != nil {
				return err
			}
			_ = events.Append(ws, events.Event{
				Type: events.FileAttached, Task: t.ID, Title: t.Title,
				Actor: actor(), Assignee: t.Assignee,
				Data: map[string]any{"file": att.File, "mime": att.Mime},
			})
			if flagJSON {
				return printJSON(att)
			}
			fmt.Printf("%s attached attachments/%s (%s, %d bytes)\n", t.ID, att.File, att.Mime, att.Bytes)
			return nil
		},
	}
	cmd.Flags().StringVar(&caption, "caption", "", "caption for the attachment")
	return cmd
}

func newFilesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "files [TASK-ID]",
		Short: "List a task's attachments",
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
			t, err := task.Load(ws, id)
			if err != nil {
				return err
			}
			atts, err := t.Attachments()
			if err != nil {
				return err
			}
			if flagJSON {
				if atts == nil {
					atts = []*task.Attachment{}
				}
				return printJSON(atts)
			}
			if len(atts) == 0 {
				fmt.Println("No attachments.")
				return nil
			}
			for _, a := range atts {
				line := fmt.Sprintf("attachments/%s\t%s\t%d bytes", a.File, a.Mime, a.Bytes)
				if a.Caption != "" {
					line += "\t" + a.Caption
				}
				fmt.Println(line)
			}
			return nil
		},
	}
}

// splitIDAndText interprets comment args: if the first arg parses as a task id
// for this workspace, it is the task and the rest is the body; otherwise every
// arg is body (scoped to the currently attached task).
func splitIDAndText(ws *workspace.Workspace, args []string) (id, text string) {
	if len(args) == 0 {
		return "", ""
	}
	if _, ok := store.ParseIDNumber(ws.Config.Settings.IDPrefix, args[0]); ok {
		return args[0], strings.Join(args[1:], " ")
	}
	return "", strings.Join(args, " ")
}
