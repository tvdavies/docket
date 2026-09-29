package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	docketservice "github.com/tvdavies/docket/internal/service"
)

func newPluginLogsCmd() *cobra.Command {
	var follow bool
	var lines int
	command := &cobra.Command{
		Use:   "logs NAME",
		Short: "Print the output of a plugin's supervised service",
		Long: "Print stdout and stderr of the plugin's service.command, as captured by the\n" +
			"Docket service, including Docket's start, restart and health messages.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := docketservice.PluginLogPath(args[0])
			if err != nil {
				return err
			}
			file, err := os.Open(path)
			if errors.Is(err, fs.ErrNotExist) && !follow {
				return fmt.Errorf("no service log for plugin %s at %s (is it enabled with service.command, and is docket serve running?)", args[0], path)
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			out := cmd.OutOrStdout()
			var offset int64
			if file != nil {
				offset, err = printTail(file, out, lines)
				file.Close()
				if err != nil {
					return err
				}
			}
			if !follow {
				return nil
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return followLog(ctx.Done(), path, offset, out)
		},
	}
	command.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new output")
	command.Flags().IntVarP(&lines, "lines", "n", 200, "print the last N lines (0 for all)")
	return command
}

// printTail writes the last n lines of file (all when n <= 0) and returns the
// offset it read up to.
func printTail(file *os.File, out io.Writer, n int) (int64, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return 0, err
	}
	start := len(data)
	if n > 0 {
		count := 0
		for start > 0 {
			if data[start-1] == '\n' && start != len(data) {
				if count++; count == n {
					break
				}
			}
			start--
		}
	} else {
		start = 0
	}
	_, err = out.Write(data[start:])
	return int64(len(data)), err
}

// followLog polls the log for appended output. The supervisor rotates the file
// to .1 when it grows too large, so a shrinking file is read from the start.
func followLog(done <-chan struct{}, path string, offset int64, out io.Writer) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		if info, err := file.Stat(); err == nil && info.Size() < offset {
			offset = 0
		}
		if _, err := file.Seek(offset, io.SeekStart); err == nil {
			written, _ := io.Copy(out, file)
			offset += written
		}
		file.Close()
	}
}
