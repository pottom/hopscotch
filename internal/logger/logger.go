// Package logger configures the application-wide structured logger.
package logger

import (
	"io"
	"os"

	"github.com/charmbracelet/log"
)

// Init sets up the global charmbracelet/log logger.
// If logFile is non-empty the output is written to both stdout and the file (tee).
// verbose enables DEBUG level; otherwise INFO.
func Init(verbose bool, logFile string) error {
	level := log.InfoLevel
	if verbose {
		level = log.DebugLevel
	}

	out, err := openOutput(logFile)
	if err != nil {
		return err
	}

	log.SetOutput(out)
	log.SetLevel(level)
	log.SetReportTimestamp(true)
	log.SetTimeFormat("15:04:05")

	return nil
}

func openOutput(logFile string) (io.Writer, error) {
	if logFile == "" {
		return os.Stdout, nil
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	return io.MultiWriter(os.Stdout, f), nil
}
