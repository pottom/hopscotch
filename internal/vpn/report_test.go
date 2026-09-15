package vpn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadReports(t *testing.T) {
	now := time.Now().Round(0)
	var records []SessionRecord
	records = append(records, retryAfterDark("4ig", 15*time.Second, OutcomeOK, 6, now)...)
	records = append(records, retryAfterDark("4ig", 15*time.Second, OutcomeDark, 4, now)...)
	records = append(records, record("m2c", now.Add(-time.Hour), OutcomeFailed), record("m2c", now.Add(-50*time.Minute), OutcomeCut))

	path := filepath.Join(t.TempDir(), HistoryFileName)
	var b strings.Builder
	for _, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	reports := ReadReports(path, now)
	if len(reports) != 2 || reports[0].VPN != "4ig" || reports[1].VPN != "m2c" {
		t.Fatalf("reports = %+v, want 4ig then m2c", reports)
	}

	ig := reports[0]
	if ig.Sessions != 10 || ig.OK != 6 || ig.Dark != 4 {
		t.Errorf("4ig counts = sessions %d ok %d dark %d, want 10/6/4", ig.Sessions, ig.OK, ig.Dark)
	}
	if w := ig.RetriesAfterDark[0]; w.Wait != 15*time.Second || w.Worked != 6 || w.Tried != 10 {
		t.Errorf("15s wait evidence = %+v, want 6/10", w)
	}
	if ig.NextWait != 15*time.Second || !strings.Contains(ig.NextReason, "6/10") {
		t.Errorf("next wait = %v (%q), want 15s citing 6/10", ig.NextWait, ig.NextReason)
	}

	m2c := reports[1]
	if m2c.Failed != 1 || m2c.Cut != 1 || !strings.Contains(m2c.NextReason, "learning") {
		t.Errorf("m2c report = %+v, want failed 1, cut 1 and the learning default", m2c)
	}

	if got := ReadReports(filepath.Join(t.TempDir(), "missing.jsonl"), now); len(got) != 0 {
		t.Errorf("missing file: %d reports, want 0", len(got))
	}
}
