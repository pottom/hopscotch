//go:build windows

package cmd

import "fmt"

func pidListeningOnPort(port int) (int, error) {
	return 0, fmt.Errorf("port-based process detection not supported on Windows")
}
