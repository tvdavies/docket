// Command docket is a generic file-backed task store with durable context,
// event hooks, and an optional local board.
package main

import (
	"os"

	"github.com/tvdavies/docket/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
