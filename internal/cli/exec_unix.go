//go:build unix

package cli

import "syscall"

func execPassthrough(program string, argv, environment []string) error {
	return syscall.Exec(program, argv, environment)
}
