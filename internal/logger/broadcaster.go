package logger

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

const maxBufLines = 300

// Broadcaster tees log output to all subscribed SSE clients and keeps a
// rolling backlog so new clients receive recent history on connect.
// Raw ANSI codes are preserved so the browser can render them via ansi_up.
type Broadcaster struct {
	mu      sync.Mutex
	buf     []string
	clients map[chan string]struct{}
}

func newBroadcaster() *Broadcaster {
	return &Broadcaster{clients: make(map[chan string]struct{})}
}

// Write implements io.Writer. Called by the logger for every log line.
func (b *Broadcaster) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")
	if line == "" {
		return len(p), nil
	}
	b.mu.Lock()
	b.buf = append(b.buf, line)
	if len(b.buf) > maxBufLines {
		b.buf = b.buf[len(b.buf)-maxBufLines:]
	}
	for ch := range b.clients {
		select {
		case ch <- line:
		default: // drop if client is too slow
		}
	}
	b.mu.Unlock()
	return len(p), nil
}

// Subscribe returns a channel that receives new log lines. Call Unsubscribe
// when done to avoid leaking the channel.
func (b *Broadcaster) Subscribe() chan string {
	ch := make(chan string, 128)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes the channel returned by Subscribe.
func (b *Broadcaster) Unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

// Backlog returns a snapshot of the most recent log lines at or above minLevel.
func (b *Broadcaster) Backlog(minLevel log.Level) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.buf))
	for _, line := range b.buf {
		if LineLevel(line) >= minLevel {
			out = append(out, line)
		}
	}
	return out
}

// LineLevel extracts the log level from a formatted log line (with or without
// ANSI escape codes). Returns InfoLevel for unrecognised lines.
func LineLevel(line string) log.Level {
	s := ansiRe.ReplaceAllString(line, "")
	switch {
	case strings.Contains(s, " DEBU "):
		return log.DebugLevel
	case strings.Contains(s, " INFO "):
		return log.InfoLevel
	case strings.Contains(s, " WARN "):
		return log.WarnLevel
	case strings.Contains(s, " ERRO "), strings.Contains(s, " ERROR "):
		return log.ErrorLevel
	default:
		return log.InfoLevel
	}
}
