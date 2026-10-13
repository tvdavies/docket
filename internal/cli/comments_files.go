package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

func newCommentCmd() *cobra.Command {
	var fromFile string
	cmd := &cobra.Command{
		Use:   "comment [TASK-ID] [TEXT]",
		Short: "Add an immutable comment to a task",
		Long: `Comments should capture decisions, evidence, dead ends, and handoff context.
Quote multi-word TEXT as one shell argument, or use --file. TASK-ID may be omitted
only when an optional session pointer is attached.`,
		Example: `  docket comment TASK-0007 "Root cause: stale cache key"
  docket comment TASK-0007 --file ./investigation.md
  printf 'Multiline note\n' | docket comment TASK-0007 --file -`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			// Disambiguate (id, text) vs (text) using whether arg0 looks like an id.
			explicit, text := splitIDAndText(ws, args)
			if fromFile != "" && text != "" {
				return fmt.Errorf("use either comment TEXT or --file, not both")
			}
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
			operations := operationsFor(ws)
			c, err := operations.Comment(id, text)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]string{"task": id, "comment": c.File})
			}
			fmt.Printf("%s commented (%s)\n", id, c.File)
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
		Short: "Copy a file into a task's attachments",
		Long: `The source file is copied into the task folder and recorded in its
attachment manifest. "docket show" lists a task's attachments.

With one argument, PATH applies to the task attached to this session; pass
TASK-ID explicitly in scripts and agents.`,
		Example: `  docket attach-file TASK-0007 ./repro.log
  docket attach-file TASK-0007 ./screenshot.png --caption "Failure state"
  docket show TASK-0007 --view current   # lists attachments`,
		Args: cobra.RangeArgs(1, 2),
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
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read source: %w", err)
			}
			att, err := operationsFor(ws).Attach(id, filepath.Base(path), data, caption)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(att)
			}
			fmt.Printf("%s attached attachments/%s (%s, %d bytes)\n", id, att.File, att.Mime, att.Bytes)
			return nil
		},
	}
	cmd.Flags().StringVar(&caption, "caption", "", "caption for the attachment")
	return cmd
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
