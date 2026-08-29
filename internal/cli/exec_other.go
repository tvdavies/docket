//go:build !unix

package cli

import (
	"os"
	"os/exec"
)

func execPassthrough(program string, argv, environment []string) error {
	command := exec.Command(program, argv[1:]...)
	command.Env = environment
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}
