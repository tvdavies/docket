// Package tui is docket's interactive board: status lanes of task cards,
// filtering, and a task detail view that reuses the internal/ui renderer so
// it looks like `docket show`. It reads and mutates tasks only through a
// Source, which the CLI backs with the same code paths as its commands.
package tui

import (
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/task"
)

// Snapshot is the board's data at one moment.
type Snapshot struct {
	// Name labels the workspace in the title bar.
	Name string
	// Statuses are the configured lanes in workflow order.
	Statuses []string
	Tasks    []*task.Task
}

// Source loads board data and performs the actions the board offers. Move
// and Comment must emit the same events as `docket move` and `docket
// comment`; a non-empty note is shown to the user (for example handler
// warnings).
type Source interface {
	Load() (Snapshot, error)
	Detail(id string) (*bundle.Bundle, error)
	Move(id, status string) (note string, err error)
	Comment(id, text string) (note string, err error)
}
