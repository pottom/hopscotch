package vpn

import (
	"bufio"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func record(vpn string, end time.Time, outcome Outcome) SessionRecord {
	return SessionRecord{VPN: vpn, Start: end.Add(-10 * time.Second), End: end, Outcome: outcome, SinceOwn: -1, SinceAny: -1}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		n++
	}
	return n
}

func TestSessionHistoryNilSafe(t *testing.T) {
	var h *sessionHistory
	h.add(record("4ig", time.Now(), OutcomeOK))
	if got := h.snapshot(); got != nil {
		t.Errorf("snapshot of nil history = %v, want nil", got)
	}
	if r := h.startContext("4ig", time.Now()); r.SinceOwn != -1 || r.SinceAny != -1 {
		t.Errorf("startContext on nil history = %+v, want unknown gaps", r)
	}
}

func TestSessionHistoryPersistsAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "vpn-sessions.jsonl")
	now := time.Now().Round(0)

	h := newSessionHistory(path)
	h.add(record("4ig", now.Add(-time.Minute), OutcomeDark))
	h.add(record("4ig", now, OutcomeOK))

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("history file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("history file mode = %o, want 600 (it holds client IPs)", perm)
	}

	reloaded := newSessionHistory(path).snapshot()
	if len(reloaded) != 2 || reloaded[0].Outcome != OutcomeDark || reloaded[1].Outcome != OutcomeOK {
		t.Fatalf("reloaded records = %+v, want dark then ok", reloaded)
	}
}

// A damaged file must never stop the VPN from working: bad lines are skipped,
// a missing file is simply empty.
func TestSessionHistoryToleratesBrokenFile(t *testing.T) {
	dir := t.TempDir()
	if got := newSessionHistory(filepath.Join(dir, "missing.jsonl")).snapshot(); len(got) != 0 {
		t.Errorf("missing file: %d records, want 0", len(got))
	}

	path := filepath.Join(dir, "vpn-sessions.jsonl")
	good := `{"vpn":"4ig","start":"2026-09-15T10:00:00Z","end":"2026-09-15T10:00:08Z","outcome":"dark"}`
	content := "not json\n" + good + "\n{\"vpn\":\"\"}\n{truncated"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newSessionHistory(path)
	h.now = func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) }
	h.records = h.trimmed(h.records)
	if got := h.snapshot(); len(got) != 1 || got[0].Outcome != OutcomeDark {
		t.Fatalf("records = %+v, want only the one valid line", got)
	}
}

func TestSessionHistoryBoundsMemoryAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vpn-sessions.jsonl")
	h := newSessionHistory(path)
	start := time.Now().Add(-time.Hour).Round(0)
	for i := 0; i < 2*historyMaxRecords+1; i++ {
		h.add(record("4ig", start.Add(time.Duration(i)*time.Second), OutcomeOK))
	}
	if got := len(h.snapshot()); got != historyMaxRecords {
		t.Errorf("in-memory records = %d, want %d", got, historyMaxRecords)
	}
	if got := countLines(t, path); got > 2*historyMaxRecords {
		t.Errorf("file lines = %d, want compaction to keep it at most %d", got, 2*historyMaxRecords)
	}
	if got := len(newSessionHistory(path).snapshot()); got != historyMaxRecords {
		t.Errorf("reloaded records = %d, want %d", got, historyMaxRecords)
	}
}

func TestSessionHistoryDropsOldRecords(t *testing.T) {
	now := time.Now()
	h := newSessionHistory("")
	h.add(record("4ig", now.Add(-historyMaxAge-time.Hour), OutcomeOK))
	h.add(record("4ig", now.Add(-time.Hour), OutcomeOK))
	if got := len(h.snapshot()); got != 1 {
		t.Errorf("records = %d, want 1 (the one older than %v dropped)", got, historyMaxAge)
	}
}

func TestSessionHistoryStartContext(t *testing.T) {
	now := time.Now()
	h := newSessionHistory("")
	h.add(record("m2c", now.Add(-20*time.Minute), OutcomeOK)) // outside the 10-minute window
	h.add(record("4ig", now.Add(-40*time.Second), OutcomeDark))
	h.add(record("m2c", now.Add(-12*time.Second), OutcomeCut))

	r := h.startContext("4ig", now)
	if r.SinceOwn < 39 || r.SinceOwn > 41 {
		t.Errorf("SinceOwn = %.1f, want ~40", r.SinceOwn)
	}
	if r.SinceAny < 11 || r.SinceAny > 13 {
		t.Errorf("SinceAny = %.1f, want ~12", r.SinceAny)
	}
	if !r.PrevDark {
		t.Error("PrevDark = false, want true (this VPN's previous attempt was dark)")
	}
	if !r.AfterSwitch {
		t.Error("AfterSwitch = false, want true (the latest attempt was the other VPN)")
	}
	if r.Starts10m != 2 {
		t.Errorf("Starts10m = %d, want 2", r.Starts10m)
	}

	first := h.startContext("unknown", now)
	if first.SinceOwn != -1 || first.PrevDark {
		t.Errorf("VPN without history: %+v, want SinceOwn -1 and no PrevDark", first)
	}
}

func TestSessionHistoryConcurrentUse(t *testing.T) {
	h := newSessionHistory(filepath.Join(t.TempDir(), "vpn-sessions.jsonl"))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.add(record("4ig", time.Now(), OutcomeOK))
				_ = h.startContext("4ig", time.Now())
				_ = h.snapshot()
			}
		}(i)
	}
	wg.Wait()
	if got := len(h.snapshot()); got != 400 {
		t.Errorf("records = %d, want 400", got)
	}
}
