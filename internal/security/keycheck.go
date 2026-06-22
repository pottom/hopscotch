// Package security provides pre-flight safety checks.
package security

import (
	"fmt"
	"io/fs"
	"os"
)

const (
	// permRequired is the only acceptable SSH key permission mask (owner read/write only).
	permRequired fs.FileMode = 0o600
	// permReadOnly is also acceptable (owner read only).
	permReadOnly fs.FileMode = 0o400
)

// ErrInsecureKeyPermission is returned when an identity file has loose permissions.
type ErrInsecureKeyPermission struct {
	Path    string
	Current fs.FileMode
}

func (e *ErrInsecureKeyPermission) Error() string {
	return fmt.Sprintf(
		"identity file has insecure permissions\n  path=%s\n  current=%o  required=600\n  fix: chmod 600 %s",
		e.Path, e.Current, e.Path,
	)
}

// CheckKeyFiles verifies that every identity file has mode 600 or 400.
// Returns a combined error if any file fails the check.
func CheckKeyFiles(paths []string) error {
	var errs []error
	for _, p := range paths {
		if err := checkOne(p); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return joinErrors(errs)
}

func checkOne(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("identity file not found: %w", err)
	}

	mode := info.Mode().Perm()
	if mode != permRequired && mode != permReadOnly {
		return &ErrInsecureKeyPermission{Path: path, Current: mode}
	}

	return nil
}

func joinErrors(errs []error) error {
	msg := ""
	for _, e := range errs {
		if msg != "" {
			msg += "\n"
		}
		msg += e.Error()
	}
	return fmt.Errorf("%s", msg)
}
