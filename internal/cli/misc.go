package cli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/task"
)

//go:embed skill.md
var skillDoc string

func newReindexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the optional .index/ cache by scanning all tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			tasks, err := task.All(ws)
			if err != nil {
				return err
			}
			data, err := jsonIndent(taskSummaries(tasks))
			if err != nil {
				return err
			}
			path := ws.Path(".index", "tasks.json")
			if err := store.WriteAtomic(path, data, 0o644); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"indexed": len(tasks), "path": path})
			}
			fmt.Printf("Indexed %d tasks → %s\n", len(tasks), path)
			return nil
		},
	}
}

func newSkillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skill",
		Short: "Print the docket agent skill / usage doc (droppable into any harness)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print(skillDoc)
			return nil
		},
	}
}
