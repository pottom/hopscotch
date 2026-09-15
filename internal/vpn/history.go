package vpn

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

// Outcome is how a connection attempt ended.
type Outcome string

const (
	OutcomeOK     Outcome = "ok"     // reached StateConnected
	OutcomeDark   Outcome = "dark"   // tunnel up, gateway returned no traffic (see dark.go)
	OutcomeFailed Outcome = "failed" // anything else: auth, subprocess exit, no interface, wrong ping_host
	OutcomeCut    Outcome = "cut"    // ended by pause or manual reconnect before a verdict
)

// SessionRecord describes one finished connection attempt. The history of these
// is what the dark-retry policy learns from (retrypolicy.go) and what
// `hopscotch vpn stats` shows.
type SessionRecord struct {
	VPN     string    `json:"vpn"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Outcome Outcome   `json:"outcome"`

	// The situation when the attempt started.
	SinceOwn    float64 `json:"since_own_s"`  // seconds since this VPN's previous attempt ended; -1 if none
	SinceAny    float64 `json:"since_any_s"`  // seconds since any VPN's previous attempt ended; -1 if none
	PrevDark    bool    `json:"prev_dark"`    // this VPN's previous attempt was dark
	DarkStreak  int     `json:"dark_streak"`  // dark attempts of this VPN in a row before this one
	Starts10m   int     `json:"starts_10m"`   // attempts (any VPN) started in the 10 minutes before
	AfterSwitch bool    `json:"after_switch"` // the latest attempt (any VPN) belonged to another VPN

	ConnectedAfter float64 `json:"connected_after_s,omitempty"` // seconds until connected (ok only)
	IP             string  `json:"ip,omitempty"`                // client address the gateway assigned
}

// HistoryFileName is the session history file, kept in the OS cache directory
// next to the PID file.
const HistoryFileName = "vpn-sessions.jsonl"

const (
	historyMaxRecords = 500
	historyMaxAge     = 30 * 24 * time.Hour
)

// sessionHistory keeps recent SessionRecords in memory and, when path is set,
// in a JSON-lines file so what was learned survives restarts. The file lives in
// the OS cache dir: it is a measurement log, neither policy nor operator intent
// (see internal/state/AGENTS.md), and deleting it only resets the learning.
//
// Like state.PausedTracker it never returns errors: a broken or unwritable file
// must not affect connecting, so problems are logged and it carries on in
// memory. All methods are nil-receiver safe.
type sessionHistory struct {
	mu          sync.Mutex
	path        string
	now         func() time.Time
	records     []SessionRecord // oldest first
	fileRecords int             // lines currently in the file, to decide when to compact
}

func newSessionHistory(path string) *sessionHistory {
	h := &sessionHistory{path: path, now: time.Now}
	if path != "" {
		h.load()
	}
	return h
}

func (h *sessionHistory) load() {
	f, err := os.Open(h.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("vpn session history unreadable, starting empty", "path", h.path, "err", err)
		}
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	skipped := 0
	for sc.Scan() {
		h.fileRecords++
		var r SessionRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil || r.VPN == "" || r.Start.IsZero() || r.End.IsZero() {
			skipped++
			continue
		}
		h.records = append(h.records, r)
	}
	if err := sc.Err(); err != nil {
		log.Warn("vpn session history partly unreadable", "path", h.path, "err", err)
	}
	if skipped > 0 {
		log.Warn("vpn session history: skipped malformed lines", "path", h.path, "skipped", skipped)
	}
	h.records = h.trimmed(h.records)
}

// trimmed drops records older than historyMaxAge and keeps at most
// historyMaxRecords of the newest.
func (h *sessionHistory) trimmed(rs []SessionRecord) []SessionRecord {
	cutoff := h.now().Add(-historyMaxAge)
	kept := make([]SessionRecord, 0, len(rs))
	for _, r := range rs {
		if r.End.After(cutoff) {
			kept = append(kept, r)
		}
	}
	if len(kept) > historyMaxRecords {
		kept = kept[len(kept)-historyMaxRecords:]
	}
	return kept
}

// add records a finished attempt.
func (h *sessionHistory) add(r SessionRecord) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = h.trimmed(append(h.records, r))
	if h.path == "" {
		return
	}
	if h.fileRecords >= 2*historyMaxRecords {
		h.rewriteLocked()
		return
	}
	if err := appendJSONLine(h.path, r); err != nil {
		log.Warn("vpn session history not saved", "path", h.path, "err", err)
		return
	}
	h.fileRecords++
}

// rewriteLocked replaces the file with the in-memory records (atomically), so
// the append-only file never grows without bound. Callers hold mu.
func (h *sessionHistory) rewriteLocked() {
	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("vpn session history not compacted", "path", h.path, "err", err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".vpn-sessions-*")
	if err != nil {
		log.Warn("vpn session history not compacted", "path", h.path, "err", err)
		return
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename

	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	for _, r := range h.records {
		if err := enc.Encode(r); err != nil {
			tmp.Close()
			log.Warn("vpn session history not compacted", "path", h.path, "err", err)
			return
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		log.Warn("vpn session history not compacted", "path", h.path, "err", err)
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		log.Debug("vpn session history: chmod failed", "err", err)
	}
	if err := tmp.Close(); err != nil {
		log.Warn("vpn session history not compacted", "path", h.path, "err", err)
		return
	}
	if err := os.Rename(tmpName, h.path); err != nil {
		log.Warn("vpn session history not compacted", "path", h.path, "err", err)
		return
	}
	h.fileRecords = len(h.records)
}

func appendJSONLine(path string, r SessionRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// snapshot returns a copy of the records, oldest first.
func (h *sessionHistory) snapshot() []SessionRecord {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]SessionRecord(nil), h.records...)
}

// startContext fills in what is known about the situation when vpn starts an
// attempt at now. DarkStreak is left to the caller, which owns that counter.
func (h *sessionHistory) startContext(vpn string, now time.Time) SessionRecord {
	r := SessionRecord{VPN: vpn, Start: now, SinceOwn: -1, SinceAny: -1}
	if h == nil {
		return r
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	foundAny, foundOwn := false, false
	for i := len(h.records) - 1; i >= 0; i-- {
		rec := h.records[i]
		if now.Sub(rec.Start) <= 10*time.Minute {
			r.Starts10m++
		}
		if !foundAny {
			foundAny = true
			r.SinceAny = now.Sub(rec.End).Seconds()
			r.AfterSwitch = rec.VPN != vpn
		}
		if !foundOwn && rec.VPN == vpn {
			foundOwn = true
			r.SinceOwn = now.Sub(rec.End).Seconds()
			r.PrevDark = rec.Outcome == OutcomeDark
		}
	}
	return r
}
