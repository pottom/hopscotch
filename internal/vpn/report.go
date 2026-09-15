package vpn

import (
	"sort"
	"time"
)

// DarkRetryWait is the recorded evidence for one candidate wait after a dark
// session.
type DarkRetryWait struct {
	Wait        time.Duration
	Worked      int
	Tried       int
	SuccessRate float64 // the smoothed, time-decayed estimate the policy uses
}

// Report summarises the recorded sessions of one VPN, for `hopscotch vpn stats`.
type Report struct {
	VPN                             string
	Sessions, OK, Dark, Failed, Cut int
	Last                            time.Time
	RetriesAfterDark                []DarkRetryWait
	NextWait                        time.Duration // what the policy picks after a dark session now (without exploring)
	NextReason                      string
}

// ReadReports loads the session history file at path (see HistoryFileName) and
// summarises it per VPN, sorted by name. A missing or broken file yields no or
// partial reports, never an error, like the daemon's own loading.
func ReadReports(path string, now time.Time) []Report {
	h := &sessionHistory{path: path, now: func() time.Time { return now }}
	h.load()
	return buildReports(h.records, now)
}

func buildReports(records []SessionRecord, now time.Time) []Report {
	byVPN := map[string]*Report{}
	for _, r := range records {
		rep, ok := byVPN[r.VPN]
		if !ok {
			rep = &Report{VPN: r.VPN}
			byVPN[r.VPN] = rep
		}
		rep.Sessions++
		switch r.Outcome {
		case OutcomeOK:
			rep.OK++
		case OutcomeDark:
			rep.Dark++
		case OutcomeCut:
			rep.Cut++
		default:
			rep.Failed++
		}
		if r.End.After(rep.Last) {
			rep.Last = r.End
		}
	}

	reports := make([]Report, 0, len(byVPN))
	for name, rep := range byVPN {
		arms, cost, _ := darkRetryEvidence(records, name, now)
		for _, a := range arms {
			rep.RetriesAfterDark = append(rep.RetriesAfterDark, DarkRetryWait{
				Wait: a.Wait, Worked: a.RawOK, Tried: a.RawOK + a.RawNo, SuccessRate: a.successRate(),
			})
		}
		_ = cost
		d := chooseDarkRetry(records, name, 1, now, func() float64 { return 1 })
		rep.NextWait, rep.NextReason = d.Delay, d.Reason
		reports = append(reports, *rep)
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].VPN < reports[j].VPN })
	return reports
}
