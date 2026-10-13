package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func newManCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "man",
		Short: "Generate roff man pages for docket and its commands",
		Long: `Without --dir, man prints the docket(1) page to stdout. With --dir, it
writes one page per command (docket.1, docket-show.1, docket-link.1, ...) into
DIR, creating it if needed. Packagers install those files under
share/man/man1. The pages are generated from the same text as --help.`,
		Example: `  docket man | man -l -
  docket man --dir ./man
  sudo docket man --dir /usr/local/share/man/man1`,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A fresh tree has no hidden runtime help or completion commands,
			// so pages document only the user-facing surface.
			root := newRootCmd()
			prepareForMan(root)
			header := &doc.GenManHeader{Section: "1", Source: "Docket " + Version, Manual: "Docket Manual"}
			if dir == "" {
				return doc.GenMan(root, header, cmd.OutOrStdout())
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := doc.GenManTree(root, header, dir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "docket: wrote man pages to %s\n", dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "write one page per command into DIR instead of printing docket.1")
	return cmd
}

// prepareForMan drops cobra's generated-by footer and adapts help text to
// the Markdown that cobra/doc renders: lines indented by fewer than four
// spaces would otherwise reflow into the surrounding paragraph.
func prepareForMan(cmd *cobra.Command) {
	cmd.DisableAutoGenTag = true
	cmd.Long = indentBlocksForMarkdown(cmd.Long)
	for _, child := range cmd.Commands() {
		prepareForMan(child)
	}
}

// indentBlocksForMarkdown shifts each run of consecutive indented lines right
// so its least-indented line has four spaces, keeping relative alignment.
func indentBlocksForMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	indentOf := func(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }
	for start := 0; start < len(lines); {
		if strings.TrimSpace(lines[start]) == "" || indentOf(lines[start]) == 0 {
			start++
			continue
		}
		end, least := start, 4
		for ; end < len(lines) && strings.TrimSpace(lines[end]) != "" && indentOf(lines[end]) > 0; end++ {
			least = min(least, indentOf(lines[end]))
		}
		for i := start; i < end; i++ {
			lines[i] = strings.Repeat(" ", 4-least) + lines[i]
		}
		start = end
	}
	return strings.Join(lines, "\n")
}
