// nextrun_spec_test.go checks a task's next run as a spec.
//
// The property: for any now, in any zone, calculateNextRun gives a UTC
// time, the one the store compares against a UTC now, and:
//   - cron: the first whole minute after now whose wall clock, in UTC or in
//     the CRON_TZ= / TZ= zone the schedule names, matches every field. A
//     day matches when both its day of month and its weekday do, or, when
//     both fields are restricted, either one. A field is unrestricted when
//     one of its parts is * or ? with no step above 1 (robfig/cron's
//     reading; classic cron also takes */N as unrestricted). Wall clocks a
//     DST change skips never match; one it repeats matches each time. A
//     schedule with no such minute in the year it starts and the five
//     after is refused, as robfig/cron searches no further;
//   - interval: now plus the duration;
//   - once: the RFC3339 instant, whatever its offset;
//   - manual: the zero time.
//
// ValidateSchedule accepts exactly the schedules that give a run.
//
// Zones are the ones in specZones: offsets on a quarter hour, and DST
// changes by a whole hour, on the hour and away from midnight, which
// TestSpecZones checks. robfig/cron v3.0.1 gets other zones' DST days
// wrong: a change at midnight (Santiago, Havana) can give a day the
// schedule excludes, and one off the hour or by half an hour (Chatham,
// Lord Howe) a wrong hour or minute, or a skipped day.
// TestKnownDSTDivergence pins a case of each, so a fixed robfig/cron
// shows up there.
//
// The reference (refCron) is written here: it parses the five fields
// itself and scans the clock minute by minute, skipping ahead a quarter
// hour at a time while the month, day or hour can't match, which is safe
// in zones whose offsets and changes fall on a quarter hour.
//
// The spec can fail: TestSpecCanFail runs the checker with a next-run
// that reads cron in now's zone and one that can return now itself, and
// requires a counterexample of each.
//
// Trusted base: the time package (zone data, time.Parse, ParseDuration).
package scheduler

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the same zones on every machine

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
)

// nextRunFunc is the function the checker checks.
type nextRunFunc func(taskType db.TaskType, schedule string, now time.Time) (time.Time, error)

// refField is one cron field: the values it matches and whether it's
// unrestricted.
type refField struct {
	match [60]bool
	star  bool
}

// refCron is a cron schedule as the reference reads it.
type refCron struct {
	loc                        *time.Location
	min, hour, dom, month, dow refField
}

var refBounds = [5]struct {
	lo, hi int
	names  []string
}{
	{0, 59, nil},
	{0, 23, nil},
	{1, 31, nil},
	{1, 12, []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{0, 6, []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}},
}

// parseRefCron parses expr, or reports false when it isn't a schedule.
func parseRefCron(expr string) (refCron, bool) {
	c := refCron{loc: time.UTC}
	if strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		tz, rest, ok := strings.Cut(expr, " ")
		if !ok {
			return c, false
		}
		_, name, _ := strings.Cut(tz, "=")
		loc, err := time.LoadLocation(name)
		if err != nil {
			return c, false
		}
		c.loc, expr = loc, rest
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return c, false
	}
	out := [5]*refField{&c.min, &c.hour, &c.dom, &c.month, &c.dow}
	for i, f := range fields {
		if !parseRefField(f, i, out[i]) {
			return c, false
		}
	}
	return c, true
}

func parseRefValue(s string, i int) (int, bool) {
	b := refBounds[i]
	if j := slices.Index(b.names, strings.ToLower(s)); j >= 0 {
		return b.lo + j, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= b.lo && n <= b.hi
}

func parseRefField(f string, i int, out *refField) bool {
	b := refBounds[i]
	for part := range strings.SplitSeq(f, ",") {
		rng, stepText, stepped := strings.Cut(part, "/")
		step := 1
		if stepped {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return false
			}
			step = n
		}
		var lo, hi int
		switch rng {
		case "*", "?":
			lo, hi = b.lo, b.hi
			out.star = out.star || step == 1
		default:
			first, last, isRange := strings.Cut(rng, "-")
			var ok bool
			if lo, ok = parseRefValue(first, i); !ok {
				return false
			}
			hi = lo
			if isRange {
				if hi, ok = parseRefValue(last, i); !ok || hi < lo {
					return false
				}
			} else if stepped {
				hi = b.hi
			}
		}
		for v := lo; v <= hi; v += step {
			out.match[v] = true
		}
	}
	return true
}

func (c refCron) dayMatches(t time.Time) bool {
	dom, dow := c.dom.match[t.Day()], c.dow.match[int(t.Weekday())]
	if c.dom.star || c.dow.star {
		return dom && dow
	}
	return dom || dow
}

// next is the first whole minute after now that matches, or false when
// there's none in now's year in the zone and the five after.
func (c refCron) next(now time.Time) (time.Time, bool) {
	limit := now.In(c.loc).Year() + 5
	t := now.Truncate(time.Minute).Add(time.Minute)
	for {
		lt := t.In(c.loc)
		switch {
		case lt.Year() > limit:
			return time.Time{}, false
		case !c.month.match[int(lt.Month())] || !c.dayMatches(lt):
			// Jump to two hours before the next day's midnight, which a DST
			// change moves by an hour at most, then go on by quarter hours.
			if jump := time.Date(lt.Year(), lt.Month(), lt.Day()+1, 0, 0, 0, 0, c.loc).Add(-2 * time.Hour); jump.After(t) {
				t = jump
			}
			t = t.Truncate(15 * time.Minute).Add(15 * time.Minute)
		case !c.hour.match[lt.Hour()]:
			t = t.Truncate(15 * time.Minute).Add(15 * time.Minute)
		case !c.min.match[lt.Minute()]:
			t = t.Add(time.Minute)
		default:
			return t.UTC(), true
		}
	}
}

// refNextRun is the reference for every task type.
func refNextRun(taskType db.TaskType, schedule string, now time.Time) (time.Time, bool) {
	switch taskType {
	case db.TaskTypeCron:
		c, ok := parseRefCron(schedule)
		if !ok {
			return time.Time{}, false
		}
		return c.next(now)
	case db.TaskTypeInterval:
		d, err := time.ParseDuration(schedule)
		if err != nil {
			return time.Time{}, false
		}
		return now.Add(d).UTC(), true
	case db.TaskTypeOnce:
		t, err := time.Parse(time.RFC3339, schedule)
		return t.UTC(), err == nil
	case db.TaskTypeManual:
		return time.Time{}, true
	}
	return time.Time{}, false
}

// checkNextRun checks the property for one task and returns how next
// breaks it.
func checkNextRun(next nextRunFunc, taskType db.TaskType, schedule string, now time.Time) error {
	want, ok := refNextRun(taskType, schedule, now)
	got, err := next(taskType, schedule, now)
	switch {
	case !ok && err == nil:
		return fmt.Errorf("%s %q from %s: got %s, want refused", taskType, schedule, now, got)
	case !ok:
		return nil
	case err != nil:
		return fmt.Errorf("%s %q from %s: %w", taskType, schedule, now, err)
	case got.Location() != time.UTC:
		return fmt.Errorf("%s %q from %s: got %s, not in UTC", taskType, schedule, now, got)
	case !got.Equal(want):
		return fmt.Errorf("%s %q from %s: got %s, want %s", taskType, schedule, now, got, want.In(got.Location()))
	}
	return nil
}

// specZones covers UTC, DST on both sides of the equator and of the date
// line, a half-hour offset with DST (Adelaide) and a quarter-hour one
// without (Kathmandu).
var specZones = []string{
	"UTC", "America/New_York", "Europe/Bucharest", "Europe/London", "Australia/Adelaide",
	"Asia/Kathmandu", "Pacific/Auckland",
}

// specYears are the years the generated cases start in, around DST
// changes.
var specYears = []int{2018, 2026}

// zoneChanges returns when loc's offset changes in year, to the quarter
// hour.
func zoneChanges(loc *time.Location, year int) []time.Time {
	var out []time.Time
	t := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	_, prev := t.In(loc).Zone()
	for ; t.Year() == year; t = t.Add(15 * time.Minute) {
		if _, off := t.In(loc).Zone(); off != prev {
			out = append(out, t)
			prev = off
		}
	}
	return out
}

// cronGen generates cron expressions, biased toward the hours DST changes
// and the days month ends.
type cronGen struct{ r *rand.Rand }

func (g cronGen) value(i int) string {
	b := refBounds[i]
	v := b.lo + g.r.IntN(b.hi-b.lo+1)
	switch {
	case i == 1 && g.r.IntN(2) == 0:
		v = []int{0, 1, 2, 3, 23}[g.r.IntN(5)]
	case i == 2 && g.r.IntN(3) == 0:
		v = 28 + g.r.IntN(4)
	}
	if b.names != nil && g.r.IntN(4) == 0 {
		name := b.names[v-b.lo]
		if g.r.IntN(2) == 0 {
			name = strings.ToUpper(name)
		}
		return name
	}
	return strconv.Itoa(v)
}

func (g cronGen) part(i int) string {
	step := strconv.Itoa(1 + g.r.IntN(15))
	switch g.r.IntN(8) {
	case 0, 1:
		return "*"
	case 2:
		if i == 2 || i == 4 {
			return "?"
		}
		return "*/" + step
	case 3:
		return "*/" + step
	case 4:
		a, b := g.value(i), g.value(i)
		if x, _ := parseRefValue(a, i); func() bool { y, _ := parseRefValue(b, i); return x > y }() {
			a, b = b, a
		}
		return a + "-" + b
	case 5:
		return g.value(i) + "/" + step
	}
	return g.value(i)
}

func (g cronGen) field(i int) string {
	parts := []string{g.part(i)}
	for g.r.IntN(4) == 0 {
		parts = append(parts, g.part(i))
	}
	return strings.Join(parts, ",")
}

// expr returns a cron expression in zone, "UTC" giving none.
func (g cronGen) expr(zone string) string {
	fields := make([]string, 5)
	for i := range fields {
		fields[i] = g.field(i)
		if i < 2 && g.r.IntN(3) == 0 {
			fields[i] = g.value(i)
		}
	}
	expr := strings.Join(fields, " ")
	if zone != "UTC" {
		expr = []string{"CRON_TZ=", "TZ="}[g.r.IntN(2)] + zone + " " + expr
	}
	return expr
}

type specCase struct {
	taskType db.TaskType
	schedule string
	now      time.Time
}

// specCases returns the fixed cases, then n generated cron ones per zone
// and some of the other types.
func specCases(tb testing.TB, n int) []specCase {
	tb.Helper()
	nowUTC := time.Date(2026, 3, 7, 23, 59, 30, 0, time.UTC)
	east := time.FixedZone("UTC+3", 3*3600)
	west := time.FixedZone("UTC-5", -5*3600)
	cases := []specCase{
		{db.TaskTypeCron, "0 9 * * *", time.Date(2026, 3, 8, 8, 0, 0, 0, east)},
		{db.TaskTypeCron, "0 22 * * *", time.Date(2026, 3, 8, 23, 30, 0, 0, east)},
		{db.TaskTypeCron, "0 9 * * *", time.Date(2026, 3, 8, 9, 0, 0, 0, time.UTC)},
		{db.TaskTypeCron, "*/15 * * * *", time.Date(2026, 3, 8, 9, 14, 59, 999, time.UTC)},
		{db.TaskTypeCron, "0 0 13 * 5", nowUTC},
		{db.TaskTypeCron, "0 0 13 * ?", nowUTC},
		{db.TaskTypeCron, "0 0 */2 * 1", nowUTC},
		{db.TaskTypeCron, "0 0 29 2 *", nowUTC},
		{db.TaskTypeCron, "0 0 30 2 *", nowUTC},
		{db.TaskTypeCron, "0 0 31 4,6,9,11 *", nowUTC},
		{db.TaskTypeCron, "30 2 * * *", nowUTC},
		{db.TaskTypeCron, "CRON_TZ=America/New_York 30 2 * * *", time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)},
		{db.TaskTypeCron, "CRON_TZ=America/New_York 30 1 * * *", time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)},
		{db.TaskTypeCron, "CRON_TZ=America/New_York 30 1 * * *", time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)},
		{db.TaskTypeCron, "TZ=Europe/London 30 1 * * *", time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)},
		{db.TaskTypeCron, "CRON_TZ=Nowhere/Zone 0 0 * * *", nowUTC},
		{db.TaskTypeCron, "0 0 * * 7", nowUTC},
		{db.TaskTypeCron, "0 24 * * *", nowUTC},
		{db.TaskTypeCron, "@daily", nowUTC},
		{db.TaskTypeCron, "0 0 * *", nowUTC},
		{db.TaskTypeCron, "0 0 * * * *", nowUTC},
		{db.TaskTypeInterval, "90m", time.Date(2026, 3, 8, 1, 0, 0, 0, east)},
		{db.TaskTypeInterval, "1h", time.Date(2026, 3, 8, 1, 0, 0, 0, west)},
		{db.TaskTypeInterval, "soon", nowUTC},
		{db.TaskTypeOnce, "2026-03-15T10:30:00-04:00", nowUTC},
		{db.TaskTypeOnce, "2026-03-15T14:30:00Z", time.Date(2026, 3, 8, 1, 0, 0, 0, east)},
		{db.TaskTypeOnce, "2026-03-15 14:30", nowUTC},
		{db.TaskTypeManual, "", time.Date(2026, 3, 8, 1, 0, 0, 0, east)},
		{db.TaskType("hourly"), "1h", nowUTC},
	}
	r := rand.New(rand.NewPCG(3, 4))
	g := cronGen{r}
	for _, zone := range specZones {
		loc, err := time.LoadLocation(zone)
		require.NoError(tb, err)
		var nows []time.Time
		for _, y := range specYears {
			for _, ch := range zoneChanges(loc, y) {
				nows = append(nows, ch.Add(-time.Duration(r.IntN(48*60))*time.Minute))
			}
		}
		nows = append(nows, time.Date(2000+r.IntN(40), 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.Int64N(int64(366*24*time.Hour)))))
		for range n {
			now := nows[r.IntN(len(nows))]
			if r.IntN(2) == 0 {
				now = now.In(loc)
			}
			cases = append(cases, specCase{db.TaskTypeCron, g.expr(zone), now})
		}
		for range n / 10 {
			now := nows[r.IntN(len(nows))].In(loc)
			cases = append(cases,
				specCase{db.TaskTypeInterval, (time.Duration(r.Int64N(int64(1000 * time.Hour)))).String(), now},
				specCase{db.TaskTypeOnce, now.Add(time.Duration(r.Int64N(int64(1000 * time.Hour)))).Format(time.RFC3339), now.UTC()},
			)
		}
	}
	return cases
}

type NextRunSpecSuite struct {
	suite.Suite
}

func TestNextRunSpecSuite(t *testing.T) {
	suite.Run(t, new(NextRunSpecSuite))
}

// TestSpec checks the property for calculateNextRun, NextRun and
// ValidateSchedule.
func (s *NextRunSpecSuite) TestSpec() {
	for _, c := range specCases(s.T(), 50) {
		require.NoError(s.T(), checkNextRun(calculateNextRun, c.taskType, c.schedule, c.now))
		_, ok := refNextRun(c.taskType, c.schedule, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		require.Equal(s.T(), ok, ValidateSchedule(c.taskType, c.schedule) == nil, "%s %q", c.taskType, c.schedule)
		want, _ := refNextRun(c.taskType, c.schedule, c.now)
		require.True(s.T(), want.Equal(NextRun(c.taskType, c.schedule, c.now)), "%s %q", c.taskType, c.schedule)
	}
}

// TestSpecZones: specZones' offsets are on a quarter hour, and their DST
// changes are by a whole hour, on the hour and away from midnight, in
// specYears.
func (s *NextRunSpecSuite) TestSpecZones() {
	for _, zone := range specZones {
		s.Run(zone, func() {
			loc, err := time.LoadLocation(zone)
			require.NoError(s.T(), err)
			for _, y := range specYears {
				for _, ch := range zoneChanges(loc, y) {
					before, after := ch.Add(-time.Second).In(loc), ch.In(loc)
					_, offBefore := before.Zone()
					_, offAfter := after.Zone()
					require.Zero(s.T(), offAfter%(15*60), "offset at %s", after)
					require.Contains(s.T(), []int{3600, -3600}, offAfter-offBefore, "change at %s", after)
					for _, wall := range []time.Time{before.Add(time.Second), after} {
						require.Zero(s.T(), wall.Minute(), "change at %s", after)
						require.NotZero(s.T(), wall.Hour(), "change at %s", after)
					}
				}
			}
		})
	}
}

// TestKnownDSTDivergence pins where robfig/cron v3.0.1 departs from the
// reference, in zones specZones leaves out. When one fails, robfig/cron
// got it right: add the zone to specZones.
func (s *NextRunSpecSuite) TestKnownDSTDivergence() {
	cases := []struct {
		name, schedule string
		now, want, got time.Time
	}{
		{
			name:     "midnight change gives an excluded day",
			schedule: "CRON_TZ=America/Santiago 4 1 */5 * *",
			now:      time.Date(2018, 8, 11, 23, 29, 0, 0, time.UTC),
			want:     time.Date(2018, 8, 16, 4, 4, 0, 0, time.UTC),
			got:      time.Date(2018, 8, 12, 4, 4, 0, 0, time.UTC),
		},
		{
			name:     "midnight change gives an excluded weekday",
			schedule: "CRON_TZ=America/Havana 52 1 * * tue/4",
			now:      time.Date(2026, 3, 7, 6, 57, 0, 0, time.UTC),
			want:     time.Date(2026, 3, 10, 5, 52, 0, 0, time.UTC),
			got:      time.Date(2026, 3, 8, 5, 52, 0, 0, time.UTC),
		},
		{
			name:     "change off the hour gives an excluded hour",
			schedule: "TZ=Pacific/Chatham 47 2/3 * * 0-1",
			now:      time.Date(2018, 9, 29, 0, 1, 0, 0, time.UTC),
			want:     time.Date(2018, 9, 29, 16, 2, 0, 0, time.UTC),
			got:      time.Date(2018, 9, 29, 14, 2, 0, 0, time.UTC),
		},
		{
			name:     "half-hour change gives an excluded minute",
			schedule: "CRON_TZ=Australia/Lord_Howe * 11 * * *",
			now:      time.Date(2018, 10, 6, 11, 13, 0, 0, time.UTC),
			want:     time.Date(2018, 10, 7, 0, 0, 0, 0, time.UTC),
			got:      time.Date(2018, 10, 7, 0, 30, 0, 0, time.UTC),
		},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			want, ok := refNextRun(db.TaskTypeCron, c.schedule, c.now)
			require.True(s.T(), ok)
			require.Equal(s.T(), c.want, want)
			got, err := calculateNextRun(db.TaskTypeCron, c.schedule, c.now)
			require.NoError(s.T(), err)
			require.Equal(s.T(), c.got, got)
		})
	}
}

// TestSpecCanFail: the checker finds a counterexample for each broken
// next-run.
func (s *NextRunSpecSuite) TestSpecCanFail() {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	cases := []struct {
		name string
		next nextRunFunc
	}{
		{"cron in now's zone", func(taskType db.TaskType, schedule string, now time.Time) (time.Time, error) {
			if taskType != db.TaskTypeCron {
				return calculateNextRun(taskType, schedule, now)
			}
			sched, err := parser.Parse(schedule)
			if err != nil {
				return time.Time{}, err
			}
			return sched.Next(now), nil
		}},
		{"can return now", func(taskType db.TaskType, schedule string, now time.Time) (time.Time, error) {
			return calculateNextRun(taskType, schedule, now.Add(-time.Second))
		}},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			found := false
			for _, sc := range specCases(s.T(), 20) {
				if checkNextRun(c.next, sc.taskType, sc.schedule, sc.now) != nil {
					found = true
					break
				}
			}
			require.True(s.T(), found, "no counterexample")
		})
	}
}
