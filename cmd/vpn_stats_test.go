package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/pottom/hopscotch/internal/vpn"
)

func TestWriteVPNStats(t *testing.T) {
	var empty strings.Builder
	writeVPNStats(&empty, "/tmp/x.jsonl", nil)
	if !strings.Contains(empty.String(), "No VPN sessions recorded yet") {
		t.Errorf("empty output = %q", empty.String())
	}

	var out strings.Builder
	writeVPNStats(&out, "/tmp/x.jsonl", []vpn.Report{{
		VPN: "4ig", Sessions: 12, OK: 8, Dark: 4, Last: time.Now(),
		RetriesAfterDark: []vpn.DarkRetryWait{
			{Wait: 15 * time.Second, Worked: 6, Tried: 10, SuccessRate: 0.58},
			{Wait: 2 * time.Minute, Worked: 0, Tried: 0, SuccessRate: 0.5},
		},
		NextWait: 15 * time.Second, NextReason: "best so far: after ~15s waits 6/10 worked",
	}})
	for _, want := range []string{
		"4ig: 12 sessions (ok 8, dark 4, failed 0, cut 0)",
		"wait ~15s  worked 6/10  (estimate 58%)",
		"wait ~2m   worked 0/0",
		"next wait after a dark session: 15s (best so far: after ~15s waits 6/10 worked)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{15 * time.Second: "15s", time.Minute: "1m", 2 * time.Minute: "2m", 90 * time.Second: "1m30s"} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
