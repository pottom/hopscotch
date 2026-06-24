// Package logger configures the application-wide structured logger.
package logger

import (
	"io"
	"os"
	"regexp"

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
// verbose enables DEBUG level; otherwise INFO.
//
// TrueColor is always set so the Broadcaster receives ANSI-coloured lines
// (the web UI Logs tab renders them via ansi_up). When stdout is not a TTY
// (container, pipe, log aggregator) ANSI codes are stripped before writing
// to stdout so log output stays readable in tools like Loki or CloudWatch.
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
	stdout := stdoutWriter()

	if logFile == "" {
		return stdout, nil
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	return io.MultiWriter(stdout, f), nil
}

// stdoutWriter returns os.Stdout directly when it is a TTY, or an
// ANSI-stripping wrapper when it is not (pipe, container, log aggregator).
func stdoutWriter() io.Writer {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return os.Stdout
	}
	return &ansiStripper{w: os.Stdout}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type ansiStripper struct{ w io.Writer }

func (s *ansiStripper) Write(p []byte) (int, error) {
	_, err := s.w.Write(ansiRe.ReplaceAll(p, nil))
	return len(p), err
}
