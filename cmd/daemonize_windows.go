//go:build windows

package cmd

import "fmt"

func daemonize() error {
	return fmt.Errorf("daemonization is not supported on Windows; use --foreground")
}
