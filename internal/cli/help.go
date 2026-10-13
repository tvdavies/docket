package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// helpStyle decorates help text. The zero value prints plain text, which is
// what pipes, files, and agent harnesses receive.
type helpStyle struct{ colour bool }

func (s helpStyle) wrap(code, text string) string {
	if !s.colour || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s helpStyle) heading(text string) string { return s.wrap("1", text) }
func (s helpStyle) command(text string) string { return s.wrap("36", text) }
func (s helpStyle) comment(text string) string { return s.wrap("2", text) }

// helpStyleFor colours help only for an interactive terminal on stdout, and
// never when NO_COLOR is set (https://no-color.org).
func helpStyleFor(w io.Writer) helpStyle {
	if w != os.Stdout || os.Getenv("NO_COLOR") != "" {
		return helpStyle{}
	}
	return helpStyle{colour: stdoutIsTerminal()}
}

// installHelp replaces cobra's default help and usage rendering for root and
// every descendant: examples move after flags, and headings are coloured on
// a terminal. The text content is otherwise cobra's.
func installHelp(root *cobra.Command) {
	root.SetHelpFunc(func(c *cobra.Command, _ []string) {
		w := c.OutOrStdout()
		style := helpStyleFor(w)
		if text := strings.TrimRight(firstNonEmpty(c.Long, c.Short), " \t\n"); text != "" {
			fmt.Fprintf(w, "%s\n\n", text)
		}
		if c.Runnable() || c.HasSubCommands() {
			writeUsage(w, c, style)
		}
	})
	root.SetUsageFunc(func(c *cobra.Command) error {
		writeUsage(c.OutOrStderr(), c, helpStyle{})
		return nil
	})
}

func writeUsage(w io.Writer, c *cobra.Command, style helpStyle) {
	var b strings.Builder
	b.WriteString(style.heading("Usage:"))
	if c.Runnable() {
		b.WriteString("\n  " + c.UseLine())
	}
	if c.HasAvailableSubCommands() {
		b.WriteString("\n  " + c.CommandPath() + " COMMAND")
	}
	if len(c.Aliases) > 0 {
		b.WriteString("\n\n" + style.heading("Aliases:") + "\n  " + c.NameAndAliases())
	}
	if c.HasAvailableSubCommands() {
		writeSubcommands(&b, c, style)
	}
	if c.HasAvailableLocalFlags() {
		b.WriteString("\n\n" + style.heading("Flags:") + "\n" + strings.TrimRight(c.LocalFlags().FlagUsages(), " \t\n"))
	}
	if c.HasAvailableInheritedFlags() {
		b.WriteString("\n\n" + style.heading("Global flags:") + "\n" + strings.TrimRight(c.InheritedFlags().FlagUsages(), " \t\n"))
	}
	if c.HasExample() {
		b.WriteString("\n\n" + style.heading("Examples:") + "\n" + styleExample(c.Example, style))
	}
	if c.HasAvailableSubCommands() {
		fmt.Fprintf(&b, "\n\nRun \"%s COMMAND --help\" for a command's flags and examples.", c.CommandPath())
	}
	if !c.HasParent() {
		b.WriteString("\nRun \"docket docs\" for reference topics and \"docket skill\" for the agent guide.")
	}
	b.WriteString("\n")
	_, _ = io.WriteString(w, b.String())
}

func writeSubcommands(b *strings.Builder, c *cobra.Command, style helpStyle) {
	visible := func(sub *cobra.Command) bool { return sub.IsAvailableCommand() || sub.Name() == "help" }
	line := func(sub *cobra.Command) {
		b.WriteString("\n  " + style.command(fmt.Sprintf("%-*s", sub.NamePadding(), sub.Name())) + "  " + sub.Short)
	}
	groups := c.Groups()
	if len(groups) == 0 {
		b.WriteString("\n\n" + style.heading("Commands:"))
		for _, sub := range c.Commands() {
			if visible(sub) {
				line(sub)
			}
		}
		return
	}
	for _, group := range groups {
		b.WriteString("\n\n" + style.heading(group.Title))
		for _, sub := range c.Commands() {
			if sub.GroupID == group.ID && visible(sub) {
				line(sub)
			}
		}
	}
	if !c.AllChildCommandsHaveGroup() {
		b.WriteString("\n\n" + style.heading("Other commands:"))
		for _, sub := range c.Commands() {
			if sub.GroupID == "" && visible(sub) {
				line(sub)
			}
		}
	}
}

// styleExample dims shell comment lines so the commands stand out.
func styleExample(example string, style helpStyle) string {
	if !style.colour {
		return example
	}
	lines := strings.Split(example, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines[i] = style.comment(line)
		}
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
