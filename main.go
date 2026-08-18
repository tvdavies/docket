// Command docket is a file-backed, CLI-only task store that hands durable context
// between agent sessions. See DESIGN.md for the architecture.
package main

import (
	"os"

	"github.com/tvdavies/docket/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
