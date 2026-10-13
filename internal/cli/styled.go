package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/ui"
)

// Styled output is for people at a terminal only. Every caller checks
// stdoutIsTerminal() (and --json) first, so pipes, files, and agent harnesses
// keep the plain text contract byte for byte.

// flagNoPager disables paging of long styled `show` output.
var flagNoPager bool

// maxReadableWidth keeps prose comfortable on very wide terminals.
const maxReadableWidth = 110

// stdinIsTerminal reports whether stdin is an interactive terminal. It asks
// the terminal driver (isatty) rather than checking for a character device,
// which /dev/null also is.
func stdinIsTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd())
}

// terminalSize returns stdout's size, or 80x24 when it cannot be read.
func terminalSize() (width, height int) {
	width, height, err := term.GetSize(os.Stdout.Fd())
	if err != nil || width <= 0 || height <= 0 {
		return 80, 24
	}
	return width, height
}

// outputProfile is the colour profile for stdout. It honours NO_COLOR
// (colour stripped, layout kept) and TERM=dumb (no escape sequences).
func outputProfile() colorprofile.Profile {
	return colorprofile.Detect(os.Stdout, os.Environ())
}

// terminalTheme picks the palette for stdout. DOCKET_THEME=light|dark
// overrides background detection, which some terminals do not answer.
func terminalTheme(profile colorprofile.Profile) ui.Theme {
	var dark bool
	switch strings.ToLower(os.Getenv("DOCKET_THEME")) {
	case "light":
		dark = false
	case "dark":
		dark = true
	default:
		// Only ask the terminal when colour will be shown: the query is an
		// escape sequence that dumb terminals would print.
		dark = profile <= colorprofile.ASCII || !stdinIsTerminal() || lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	}
	theme := ui.NewTheme(dark)
	theme.Plain = profile <= colorprofile.ASCII
	return theme
}

// printTaskStyled renders a task for a terminal, paging it when it is taller
// than the screen.
func printTaskStyled(value ui.Task) error {
	profile := outputProfile()
	width, height := terminalSize()
	renderer := ui.Renderer{Theme: terminalTheme(profile), Width: min(width, maxReadableWidth)}
	return page(renderer.Document(value), profile, height, flagNoPager)
}

// printTaskTableStyled prints a coloured table sized to the terminal.
func printTaskTableStyled(tasks []*task.Task) {
	profile := outputProfile()
	width, _ := terminalSize()
	writeProfiled(os.Stdout, profile, terminalTheme(profile).TaskTable(tasks, width))
}

// writeProfiled writes styled text, downsampling or stripping escape
// sequences to what profile supports.
func writeProfiled(w io.Writer, profile colorprofile.Profile, text string) {
	writer := &colorprofile.Writer{Forward: w, Profile: profile}
	_, _ = io.WriteString(writer, text)
}

// page writes content to stdout, through $PAGER (default "less -R") when it
// does not fit on one screen. Paging is skipped with --no-pager, for
// TERM=dumb, and when PAGER is set to an empty string or "cat".
func page(content string, profile colorprofile.Profile, height int, noPager bool) error {
	return pageTo(os.Stdout, content, profile, height, noPager)
}

// pageTo is page writing to out. When the pager cannot be run (not
// installed, not executable) the content is written to out directly, so it
// is never lost. Quitting the pager early is not an error; a pager that
// fails is.
func pageTo(out io.Writer, content string, profile colorprofile.Profile, height int, noPager bool) error {
	var rendered bytes.Buffer
	writeProfiled(&rendered, profile, content)
	pager, usePager := pagerCommand()
	if noPager || !usePager || os.Getenv("TERM") == "dumb" || strings.Count(rendered.String(), "\n") < height || !pagerAvailable(pager) {
		_, err := out.Write(rendered.Bytes())
		return err
	}
	cmd := pagerProcess(pager)
	cmd.Stdin = bytes.NewReader(rendered.Bytes())
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if _, ok := os.LookupEnv("LESS"); !ok {
		// Like git: raw colour codes, quit if one screen, keep the screen.
		cmd.Env = append(cmd.Env, "LESS=FRX")
	}
	if err := cmd.Start(); err != nil {
		_, err := out.Write(rendered.Bytes())
		return err
	}
	return pagerResult(cmd.Wait(), pager, out, rendered.Bytes())
}

// pagerResult interprets the pager's exit. The shell exits 126 or 127 when
// it cannot run the pager, which therefore printed nothing: the content goes
// to out directly. A pager closing its input early (q in less) or ended by a
// signal is a normal way to stop reading.
func pagerResult(err error, pager string, out io.Writer, content []byte) error {
	if err == nil || errors.Is(err, syscall.EPIPE) {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch code := exit.ExitCode(); code {
		case -1: // terminated by a signal
			return nil
		case 126, 127:
			_, werr := out.Write(content)
			return werr
		default:
			return fmt.Errorf("pager %q exited with status %d", pager, code)
		}
	}
	return fmt.Errorf("pager %q: %w", pager, err)
}

// pagerAvailable reports whether the pager's program can be found. Leading
// VAR=value assignments are skipped, as the shell would.
func pagerAvailable(pager string) bool {
	for _, field := range strings.Fields(pager) {
		if strings.Contains(field, "=") && !strings.ContainsAny(field, "/") {
			continue
		}
		_, err := exec.LookPath(field)
		return err == nil
	}
	return false
}

// pagerCommand returns the pager to run and whether paging is enabled.
func pagerCommand() (string, bool) {
	pager, set := os.LookupEnv("PAGER")
	if !set {
		return "less -R", true
	}
	pager = strings.TrimSpace(pager)
	if pager == "" || pager == "cat" {
		return "", false
	}
	return pager, true
}

func pagerProcess(pager string) *exec.Cmd {
	if shell, err := exec.LookPath("sh"); err == nil {
		return exec.Command(shell, "-c", pager)
	}
	fields := strings.Fields(pager)
	return exec.Command(fields[0], fields[1:]...)
}
