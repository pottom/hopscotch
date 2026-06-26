// Package logger configures the application-wide structured logger.
package logger

import (
	"io"
	"os"

	"github.com/charmbracelet/log"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

var defaultBroadcaster = newBroadcaster()

// GetBroadcaster returns the singleton log broadcaster. Both the web UI SSE
// handler and the TUI log pane subscribe to this to receive live log lines.
func GetBroadcaster() *Broadcaster {
	return defaultBroadcaster
}

// Init sets up the global charmbracelet/log logger.
// If logFile is non-empty the output is written to both stdout and the file (tee).
// verbose controls the stdout/file threshold; the Broadcaster always receives
// DEBUG so the TUI and web UI can filter freely on the client side.
//
// TrueColor is always set so the Broadcaster receives ANSI-coloured lines
// (the web UI Logs tab renders them via ansi_up). When stdout is not a TTY
// (container, pipe, log aggregator) ANSI codes are stripped before writing
// to stdout so log output stays readable in tools like Loki or CloudWatch.
func Init(verbose bool, logFile string) error {
	stdoutLevel := log.InfoLevel
	if verbose {
		stdoutLevel = log.DebugLevel
	}

	out, err := openOutput(logFile, stdoutLevel)
	if err != nil {
		return err
	}

	// Always log at DEBUG so the broadcaster captures every level.
	// Stdout/file output is filtered by levelFilterWriter to respect --verbose.
	log.SetOutput(io.MultiWriter(out, defaultBroadcaster))
	log.SetColorProfile(termenv.TrueColor)
	log.SetStyles(buildStyles())
	log.SetLevel(log.DebugLevel)
	log.SetReportTimestamp(true)
	log.SetTimeFormat("15:04:05")

	return nil
}

func openOutput(logFile string, level log.Level) (io.Writer, error) {
	stdout := &levelFilterWriter{w: stdoutWriter(), level: level}

	if logFile == "" {
		return stdout, nil
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	return io.MultiWriter(stdout, &levelFilterWriter{w: f, level: level}), nil
}

// levelFilterWriter passes only log lines at or above the configured level.
// It parses the level from the formatted (ANSI-coloured) line.
type levelFilterWriter struct {
	w     io.Writer
	level log.Level
}

func (f *levelFilterWriter) Write(p []byte) (int, error) {
	if LineLevel(string(p)) >= f.level {
		return f.w.Write(p)
	}
	return len(p), nil
}

// stdoutWriter returns os.Stdout directly when it is a TTY, or an
// ANSI-stripping wrapper when it is not (pipe, container, log aggregator).
func stdoutWriter() io.Writer {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return os.Stdout
	}
	return &ansiStripper{w: os.Stdout}
}

type ansiStripper struct{ w io.Writer }

func (s *ansiStripper) Write(p []byte) (int, error) {
	_, err := s.w.Write(ansiRe.ReplaceAll(p, nil))
	return len(p), err
}
