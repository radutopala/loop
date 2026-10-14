package backup

import (
	"fmt"
	"time"

	"github.com/radutopala/loop/internal/config"
)

// retain picks the backups to keep from backups, newest first: the newest
// KeepLast, and the newest of each of the newest KeepDaily days,
// KeepWeekly weeks and KeepMonthly months that have one, in UTC. The
// newest is always kept.
func retain(backups []Backup, r config.BackupRetention) map[string]bool {
	keep := map[string]bool{}
	for i, b := range backups {
		if i == 0 || i < r.KeepLast {
			keep[b.File] = true
		}
	}
	tiers := []struct {
		n      int
		period func(time.Time) string
	}{
		{r.KeepDaily, func(t time.Time) string { return t.Format(time.DateOnly) }},
		{r.KeepWeekly, func(t time.Time) string {
			year, week := t.ISOWeek()
			return fmt.Sprintf("%d-W%02d", year, week)
		}},
		{r.KeepMonthly, func(t time.Time) string { return t.Format("2006-01") }},
	}
	for _, tier := range tiers {
		seen := map[string]bool{}
		for _, b := range backups {
			if len(seen) >= tier.n {
				break
			}
			if p := tier.period(b.At.UTC()); !seen[p] {
				seen[p] = true
				keep[b.File] = true
			}
		}
	}
	return keep
}
