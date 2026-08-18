package cli

import (
	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/luahook"
)

// newLuaHookCmd is an internal process-isolation boundary used by the handler
// runner. It is deliberately hidden: users configure `lua:` handlers rather
// than invoking this command directly.
func newLuaHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__lua-hook SCRIPT",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			return luahook.Run(luahook.Options{
				Context:   cmd.Context(),
				Workspace: ws,
				Script:    args[0],
				Input:     cmd.InOrStdin(),
				Output:    cmd.OutOrStdout(),
				Error:     cmd.ErrOrStderr(),
			})
		},
	}
}
