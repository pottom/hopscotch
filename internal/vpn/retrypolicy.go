package vpn

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Choosing when to retry after a dark session (see dark.go).
//
// Whether the next session will be dark can't be seen in advance, so the only
// way to pick a good wait is to learn from past attempts. Every attempt that
// followed a dark one is an observation "after waiting ~W, the session worked
// / was dark again"; manual pause/resume retries count too. For each candidate
// wait the policy estimates the success rate (time-decayed, with a uniform
// prior) and picks the wait with the lowest expected time to recovery:
//
//	E(wait) = (wait + cost of a dark attempt) / P(success after wait)
//
// With little evidence it uses a fixed default. Now and then it tries a wait it
// knows little about, so one bad early experience can't lock it in. Hard limits
// that are never learned stay in force on top: the session gate
// (sessiongate.go) and the floor after a long dark streak below.
var darkRetryArms = []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute}

const (
	darkRetryDefault       = 15 * time.Second // measured: an immediate manual retry often worked
	darkRetryMinEvidence   = 8.0              // effective observations before the history is trusted
	darkRetryHalfLife      = 7 * 24 * time.Hour
	darkRetryExploreRate   = 0.1
	darkRetryExploreBelow  = 3 // arms with fewer raw observations are exploration candidates
	darkRetryFloorStreak   = 6
	darkRetryFloor         = 2 * time.Minute // after ~25 sessions in ten minutes the gateway stayed dark for 16 minutes
	darkAttemptDefaultCost = 8 * time.Second
)

// darkRetryArm is the learned evidence for one candidate wait.
type darkRetryArm struct {
	Wait         time.Duration
	OK, Dark     float64 // time-decayed counts
	RawOK, RawNo int     // undecayed counts, for display
}

func (a darkRetryArm) successRate() float64 { return (1 + a.OK) / (2 + a.OK + a.Dark) }

func (a darkRetryArm) expected(cost time.Duration) time.Duration {
	return time.Duration(float64(a.Wait+cost) / a.successRate())
}

// retryDecision is the chosen wait and why.
type retryDecision struct {
	Delay  time.Duration
	Reason string // short, shown in both UIs
	Detail string // per-candidate evidence, for the log
}

// darkRetryArmIndex maps an observed wait to the nearest candidate.
func darkRetryArmIndex(wait time.Duration) int {
	switch {
	case wait < 22500*time.Millisecond:
		return 0
	case wait < 45*time.Second:
		return 1
	case wait < 90*time.Second:
		return 2
	default:
		return 3
	}
}

// darkRetryEvidence collects, for vpn, the outcomes of attempts that followed a
// dark attempt, bucketed by how long was waited, plus the typical duration of a
// dark attempt and the total effective evidence.
func darkRetryEvidence(records []SessionRecord, vpn string, now time.Time) (arms []darkRetryArm, cost time.Duration, evidence float64) {
	arms = make([]darkRetryArm, len(darkRetryArms))
	for i, w := range darkRetryArms {
		arms[i].Wait = w
	}
	var darkDurations []time.Duration
	for _, r := range records {
		if r.VPN != vpn {
			continue
		}
		if r.Outcome == OutcomeDark && r.End.After(r.Start) {
			darkDurations = append(darkDurations, r.End.Sub(r.Start))
		}
		if !r.PrevDark || r.SinceOwn < 0 || (r.Outcome != OutcomeOK && r.Outcome != OutcomeDark) {
			continue
		}
		age := now.Sub(r.End)
		if age < 0 {
			age = 0
		}
		weight := math.Pow(0.5, float64(age)/float64(darkRetryHalfLife))
		arm := &arms[darkRetryArmIndex(time.Duration(r.SinceOwn*float64(time.Second)))]
		if r.Outcome == OutcomeOK {
			arm.OK += weight
			arm.RawOK++
		} else {
			arm.Dark += weight
			arm.RawNo++
		}
		evidence += weight
	}

	cost = darkAttemptDefaultCost
	if len(darkDurations) > 0 {
		slices.Sort(darkDurations)
		cost = min(max(darkDurations[len(darkDurations)/2], 3*time.Second), time.Minute)
	}
	return arms, cost, evidence
}

// chooseDarkRetry decides how long vpn waits before its next session after
// `streak` dark sessions in a row. rnd returns values in [0,1) and drives
// exploration; tests pass a fixed sequence.
func chooseDarkRetry(records []SessionRecord, vpn string, streak int, now time.Time, rnd func() float64) retryDecision {
	arms, cost, evidence := darkRetryEvidence(records, vpn, now)

	var d retryDecision
	if evidence < darkRetryMinEvidence {
		seen := 0
		for _, a := range arms {
			seen += a.RawOK + a.RawNo
		}
		d.Delay = darkRetryDefault
		d.Reason = fmt.Sprintf("default wait while learning (%d retries after dark sessions seen)", seen)
	} else {
		// Only waits with evidence of their own compete on expected recovery
		// time: an untried wait would otherwise win on its optimistic prior
		// alone (0/0 → 50 %) against a well-proven one. Untried waits are only
		// reached through the deliberate exploration below.
		best, why := -1, "best so far"
		for i, a := range arms {
			if a.RawOK+a.RawNo < darkRetryExploreBelow {
				continue
			}
			if best < 0 || a.expected(cost) < arms[best].expected(cost) {
				best = i
			}
		}
		if best < 0 {
			best, why = darkRetryArmIndex(darkRetryDefault), "too little evidence per wait, default"
		}
		chosen := best
		if rnd() < darkRetryExploreRate {
			var candidates []int
			for i, a := range arms {
				if i != best && a.RawOK+a.RawNo < darkRetryExploreBelow {
					candidates = append(candidates, i)
				}
			}
			if len(candidates) > 0 {
				chosen = candidates[int(rnd()*float64(len(candidates)))%len(candidates)]
				why = "trying a wait with little evidence"
			}
		}
		a := arms[chosen]
		d.Delay = a.Wait
		d.Reason = fmt.Sprintf("%s: after ~%s waits %d/%d worked", why, fmtWait(a.Wait), a.RawOK, a.RawOK+a.RawNo)
	}

	if streak >= darkRetryFloorStreak && d.Delay < darkRetryFloor {
		d.Delay = darkRetryFloor
		d.Reason = fmt.Sprintf("%d dark sessions in a row, pausing new sessions for %s", streak, fmtWait(darkRetryFloor))
	}

	var detail strings.Builder
	for i, a := range arms {
		if i > 0 {
			detail.WriteString(", ")
		}
		fmt.Fprintf(&detail, "~%s %d/%d (p=%.2f, E=%s)", fmtWait(a.Wait), a.RawOK, a.RawOK+a.RawNo,
			a.successRate(), a.expected(cost).Round(time.Second))
	}
	fmt.Fprintf(&detail, "; dark attempt cost %s; evidence %.1f", cost.Round(time.Second), evidence)
	d.Detail = detail.String()
	return d
}

// fmtWait prints 15s, 30s, 1m, 2m rather than 1m0s.
func fmtWait(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.Round(time.Second).String()
}
