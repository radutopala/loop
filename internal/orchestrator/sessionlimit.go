// sessionlimit.go handles Claude "session limit" errors by scheduling a
// one-shot retry at the announced reset time. Unlike transient rate limits
// (handled by in-process backoff in the container runner), a session limit
// resets hours later, so the retry is persisted as a scheduled task that
// survives daemon restarts. The reset instant is parsed from the human-readable
// error string — Claude provides no machine-readable timestamp.
package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
)

// sessionLimitTemplateName tags the scheduled retry so duplicates (several
// queued messages hitting the limit in one drain) are not stacked.
const sessionLimitTemplateName = "session-limit-auto-continue"

// resetTimeRe matches the reset clause in a session-limit error, e.g.
// "resets 11:30pm (Europe/Bucharest)" or "resets 8am (UTC)".
var resetTimeRe = regexp.MustCompile(`(?i)resets\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*\(([^)]+)\)`)

// isSessionLimitError reports whether the error text is a Claude session-limit
// message (as opposed to a weekly "usage limit reached", which has no intraday
// reset time and is therefore terminal).
func isSessionLimitError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "hit your session limit") || strings.Contains(m, "hit your limit")
}

// parseSessionLimitReset extracts the reset instant from a session-limit error.
// The time is a 12-hour wall clock in the named IANA timezone; the result is
// the next occurrence strictly after now (today, or tomorrow if already past).
// Returns ok=false when the clause is absent or the timezone cannot be loaded.
func parseSessionLimitReset(msg string, now time.Time) (time.Time, bool) {
	m := resetTimeRe.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	hour, err := strconv.Atoi(m[1])
	if err != nil || hour < 1 || hour > 12 {
		return time.Time{}, false
	}
	minute := 0
	if m[2] != "" {
		minute, err = strconv.Atoi(m[2])
		if err != nil || minute > 59 {
			return time.Time{}, false
		}
	}
	// 12-hour → 24-hour: 12am = 00:00, 12pm = 12:00.
	switch strings.ToLower(m[3]) {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour != 12 {
			hour += 12
		}
	}
	loc, err := time.LoadLocation(strings.TrimSpace(m[4]))
	if err != nil {
		return time.Time{}, false
	}
	nowLocal := now.In(loc)
	reset := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), hour, minute, 0, 0, loc)
	if !reset.After(now) {
		reset = reset.Add(24 * time.Hour)
	}
	return reset, true
}

// maybeScheduleSessionLimitRetry inspects a failed-run error and, when it is a
// session-limit error with a parseable reset time, schedules a one-shot task
// that resumes the channel at that time with a "continue" prompt. It returns a
// human-readable notice and true when a retry was scheduled; otherwise "" and
// false (the caller falls through to its generic error handling).
func (o *Orchestrator) maybeScheduleSessionLimitRetry(ctx context.Context, msg *bot.IncomingMessage, errMsg string) (string, bool) {
	if !o.currentConfig().AgentRetry.SessionLimitAutoContinue {
		return "", false
	}
	if !isSessionLimitError(errMsg) {
		return "", false
	}
	reset, ok := parseSessionLimitReset(errMsg, o.timeNow())
	if !ok {
		return "", false
	}

	// Dedupe: don't stack retries when several queued messages hit the limit.
	if existing, err := o.scheduler.ListTasks(ctx, msg.ChannelID); err == nil {
		for _, t := range existing {
			if t.TemplateName == sessionLimitTemplateName && t.Enabled {
				return "", false
			}
		}
	}

	task := &db.ScheduledTask{
		ChannelID:    msg.ChannelID,
		GuildID:      msg.GuildID,
		Schedule:     reset.Format(time.RFC3339),
		Type:         db.TaskTypeOnce,
		Prompt:       "continue",
		Enabled:      true,
		TemplateName: sessionLimitTemplateName,
	}
	if _, err := o.scheduler.AddTask(ctx, task); err != nil {
		o.logger.Error("scheduling session-limit retry", "error", err, "channel_id", msg.ChannelID)
		return "", false
	}

	wait := reset.Sub(o.timeNow()).Round(time.Minute)
	o.logger.Info("scheduled session-limit auto-continue", "channel_id", msg.ChannelID, "reset_at", reset.Format(time.RFC3339), "wait", wait)
	return fmt.Sprintf("⏳ Session limit reached — I'll automatically continue at %s (in %s).", reset.Format("3:04pm (MST)"), formatWait(wait)), true
}

// formatWait renders a duration as a compact "9h27m" / "45m" string.
func formatWait(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
