// Package logger configures the application-wide structured logger.
package logger

import (
	"io"
	"os"

	"github.com/charmbracelet/log"
	"github.com/muesli/termenv"
)

var defaultBroadcaster = newBroadcaster()

// GetBroadcaster returns the singleton log broadcaster. Both the web UI SSE
// handler and the TUI log pane subscribe to this to receive live log lines.
func GetBroadcaster() *Broadcaster {
	return defaultBroadcaster
}

// Init sets up the global charmbracelet/log logger.
// If logFile is non-empty the output is written to both stdout and the file (tee).
// verbose enables DEBUG level; otherwise INFO.
//
// TrueColor is always forced so the Broadcaster receives ANSI-coloured lines
// regardless of whether stdout is a TTY (e.g. in daemon mode the process has
// no terminal, but the web UI Logs tab still needs colour via ansi_up).
// In daemon mode stdout is /dev/null so sending ANSI there is harmless.
func Init(verbose bool, logFile string) error {
	level := log.InfoLevel
	if verbose {
		level = log.DebugLevel
	}

	out, err := openOutput(logFile)
	if err != nil {
		return err
	}

	log.SetOutput(io.MultiWriter(out, defaultBroadcaster))
	log.SetColorProfile(termenv.TrueColor)
	log.SetStyles(buildStyles())
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
