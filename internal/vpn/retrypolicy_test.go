package vpn

import (
	"strings"
	"testing"
	"time"
)

// retryAfterDark builds n attempts of vpn that each followed a dark attempt
// after waiting `wait`, with the given outcome, ending around now.
func retryAfterDark(vpn string, wait time.Duration, outcome Outcome, n int, now time.Time) []SessionRecord {
	var rs []SessionRecord
	for i := 0; i < n; i++ {
		end := now.Add(-time.Duration(i+1) * time.Minute)
		rs = append(rs, SessionRecord{
			VPN: vpn, Start: end.Add(-8 * time.Second), End: end, Outcome: outcome,
			PrevDark: true, SinceOwn: wait.Seconds(), SinceAny: wait.Seconds(),
		})
	}
	return rs
}

func never() float64 { return 0.99 } // never explore

func TestDarkRetryDefaultsWhileLearning(t *testing.T) {
	now := time.Now()
	records := retryAfterDark("4ig", 2*time.Minute, OutcomeOK, 3, now)

	d := chooseDarkRetry(records, "4ig", 2, now, never)
	if d.Delay != darkRetryDefault {
		t.Errorf("delay = %v, want the default %v with only 3 observations", d.Delay, darkRetryDefault)
	}
	if !strings.Contains(d.Reason, "learning") {
		t.Errorf("reason = %q, want it to say it is still learning", d.Reason)
	}
}

// Quick retries win whenever they work reasonably often: a failed try only
// costs the wait plus a few seconds of detection.
func TestDarkRetryPrefersShortWaitWhenItWorks(t *testing.T) {
	now := time.Now()
	var records []SessionRecord
	records = append(records, retryAfterDark("4ig", 15*time.Second, OutcomeOK, 6, now)...)
	records = append(records, retryAfterDark("4ig", 15*time.Second, OutcomeDark, 4, now)...)
	records = append(records, retryAfterDark("4ig", 2*time.Minute, OutcomeOK, 9, now)...)
	records = append(records, retryAfterDark("4ig", 2*time.Minute, OutcomeDark, 1, now)...)

	d := chooseDarkRetry(records, "4ig", 2, now, never)
	if d.Delay != 15*time.Second {
		t.Fatalf("delay = %v, want 15s (6/10 success at 15s beats 9/10 at 2m on expected recovery time); detail: %s", d.Delay, d.Detail)
	}
	if !strings.Contains(d.Reason, "6/10") {
		t.Errorf("reason = %q, want the evidence for the chosen wait", d.Reason)
	}
}

// When short retries practically never work, waiting longer is faster overall.
func TestDarkRetryLearnsToWaitLonger(t *testing.T) {
	now := time.Now()
	var records []SessionRecord
	records = append(records, retryAfterDark("4ig", 15*time.Second, OutcomeDark, 10, now)...)
	records = append(records, retryAfterDark("4ig", 2*time.Minute, OutcomeOK, 9, now)...)
	records = append(records, retryAfterDark("4ig", 2*time.Minute, OutcomeDark, 1, now)...)

	d := chooseDarkRetry(records, "4ig", 2, now, never)
	if d.Delay != 2*time.Minute {
		t.Fatalf("delay = %v, want 2m (0/10 at 15s, 9/10 at 2m); detail: %s", d.Delay, d.Detail)
	}
}

// Enough evidence overall but too little for any single wait: nothing is proven,
// so stay on the default rather than trust a 2-observation estimate.
func TestDarkRetryThinEvidenceKeepsDefault(t *testing.T) {
	now := time.Now()
	var records []SessionRecord
	for _, w := range darkRetryArms {
		records = append(records, retryAfterDark("4ig", w, OutcomeOK, 1, now)...)
		records = append(records, retryAfterDark("4ig", w, OutcomeDark, 1, now)...)
	}
	// 2 minutes with 2/2 would look perfect, but 2 observations prove nothing.
	records = append(records[:6], retryAfterDark("4ig", 2*time.Minute, OutcomeOK, 2, now)...)

	// With 4 waits and fewer than 3 observations each, the total can't reach
	// darkRetryMinEvidence either, so this is the "learning" branch — either
	// way the result must be the default, never a claimed best.
	d := chooseDarkRetry(records, "4ig", 2, now, never)
	if d.Delay != darkRetryDefault || strings.Contains(d.Reason, "best so far") {
		t.Errorf("decision = %+v, want the default because no wait has %d observations", d, darkRetryExploreBelow)
	}
}

func TestDarkRetryIgnoresIrrelevantRecords(t *testing.T) {
	now := time.Now()
	var records []SessionRecord
	// Plenty of "long waits work" evidence, but none of it counts for 4ig:
	// another VPN, attempts not after a dark one, or no verdict.
	records = append(records, retryAfterDark("m2c", 2*time.Minute, OutcomeOK, 20, now)...)
	for _, r := range retryAfterDark("4ig", 2*time.Minute, OutcomeOK, 20, now) {
		r.PrevDark = false
		records = append(records, r)
	}
	records = append(records, retryAfterDark("4ig", 2*time.Minute, OutcomeCut, 20, now)...)

	if d := chooseDarkRetry(records, "4ig", 2, now, never); d.Delay != darkRetryDefault || !strings.Contains(d.Reason, "learning") {
		t.Errorf("decision = %+v, want the learning default", d)
	}
}

// Behaviour changes over weeks; month-old evidence must not outweigh fresh data.
func TestDarkRetryForgetsOldEvidence(t *testing.T) {
	now := time.Now()
	old := retryAfterDark("4ig", 2*time.Minute, OutcomeOK, 30, now.Add(-35*24*time.Hour))
	if d := chooseDarkRetry(old, "4ig", 2, now, never); d.Delay != darkRetryDefault {
		t.Errorf("delay = %v, want the default: 30 five-week-old observations weigh less than %.0f", d.Delay, darkRetryMinEvidence)
	}
}

func TestDarkRetryExploresUnderSampledWaits(t *testing.T) {
	now := time.Now()
	records := retryAfterDark("4ig", 15*time.Second, OutcomeOK, 10, now) // only the 15s wait has evidence

	calls := 0
	rnd := func() float64 {
		calls++
		if calls == 1 {
			return 0.01 // explore
		}
		return 0.0 // first candidate
	}
	d := chooseDarkRetry(records, "4ig", 1, now, rnd)
	if d.Delay == 15*time.Second || !strings.Contains(d.Reason, "little evidence") {
		t.Errorf("decision = %+v, want an under-sampled wait tried on purpose", d)
	}
}

// However well quick retries did, a long dark streak always pauses new sessions.
func TestDarkRetryFloorAfterLongStreak(t *testing.T) {
	now := time.Now()
	records := retryAfterDark("4ig", 15*time.Second, OutcomeOK, 20, now)

	if d := chooseDarkRetry(records, "4ig", darkRetryFloorStreak-1, now, never); d.Delay != 15*time.Second {
		t.Errorf("streak %d: delay = %v, want the learned 15s", darkRetryFloorStreak-1, d.Delay)
	}
	d := chooseDarkRetry(records, "4ig", darkRetryFloorStreak, now, never)
	if d.Delay != darkRetryFloor || !strings.Contains(d.Reason, "in a row") {
		t.Errorf("streak %d: decision = %+v, want the %v floor", darkRetryFloorStreak, d, darkRetryFloor)
	}
}

func TestDarkRetryArmIndex(t *testing.T) {
	for wait, want := range map[time.Duration]int{
		0: 0, 11 * time.Second: 0, 22 * time.Second: 0, 23 * time.Second: 1,
		44 * time.Second: 1, 45 * time.Second: 2, 89 * time.Second: 2, 90 * time.Second: 3, 10 * time.Minute: 3,
	} {
		if got := darkRetryArmIndex(wait); got != want {
			t.Errorf("darkRetryArmIndex(%v) = %d, want %d", wait, got, want)
		}
	}
}
