package bot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/events"
)

// On chat platforms an ask or plan card is posted as a message followed by a
// message of buttons. A click stands for a reply (an option number or one of
// these keywords), and typing the reply works the same way.
const (
	AskReplySkip     = "skip"
	PlanReplyApprove = "approve"
	PlanReplyReject  = "reject"

	askReplyHint            = "Pick an option, or reply with your own answer."
	askReplyHintMultiSelect = "Reply with the option numbers (like `1, 3`) or your own answer."
	askReplyHintMulti       = "Reply with your answers."
	planReplyHint           = "Approve or reject, or reply with what to change."

	// cardActionPrefix starts a card button's action ID; see CardActionID.
	cardActionPrefix = "card:"
	// maxCardButtons is the most buttons one card message carries (Discord
	// allows five rows of five; Slack 25 elements per actions block).
	maxCardButtons = 25
	// maxCardButtonLabel is the longest button label (Slack's limit is 75).
	maxCardButtonLabel = 75
)

// CardButtonStyle is a card button's emphasis.
type CardButtonStyle int

const (
	CardButtonDefault CardButtonStyle = iota
	CardButtonPrimary
	CardButtonDanger
)

// CardButton is one button on an ask or plan card. Choice is the reply the
// click stands for.
type CardButton struct {
	Label  string
	Choice string
	Style  CardButtonStyle
}

// AskCardButtons returns an ask card's buttons: one per option when it is a
// single question with a single answer, then Skip. Other asks are answered
// by reply, so they only get Skip.
func AskCardButtons(data events.AskUserQuestionEventData) []CardButton {
	var btns []CardButton
	if len(data.Questions) == 1 && !data.Questions[0].MultiSelect && len(data.Questions[0].Options) < maxCardButtons {
		for i, opt := range data.Questions[0].Options {
			btns = append(btns, CardButton{Label: cardButtonLabel(opt.Label), Choice: strconv.Itoa(i + 1)})
		}
	}
	return append(btns, CardButton{Label: "Skip", Choice: AskReplySkip})
}

// PlanCardButtons returns a plan card's buttons.
func PlanCardButtons() []CardButton {
	return []CardButton{
		{Label: "Approve", Choice: PlanReplyApprove, Style: CardButtonPrimary},
		{Label: "Reject", Choice: PlanReplyReject, Style: CardButtonDanger},
	}
}

// cardButtonLabel shortens a label to the platforms' button limit.
func cardButtonLabel(label string) string {
	r := []rune(label)
	if len(r) <= maxCardButtonLabel {
		return label
	}
	return string(r[:maxCardButtonLabel-1]) + "…"
}

// CardActionID is a card button's action ID: "card:<channelID>:<cardID>:<choice>".
// The channel ID may itself contain colons (Slack threads), so it is parsed
// from the end; card IDs and choices never contain one.
func CardActionID(channelID, cardID, choice string) string {
	return cardActionPrefix + channelID + ":" + cardID + ":" + choice
}

// ParseCardActionID splits an action ID built by CardActionID. ok is false
// for any other ID.
func ParseCardActionID(id string) (channelID, cardID, choice string, ok bool) {
	rest, found := strings.CutPrefix(id, cardActionPrefix)
	if !found {
		return "", "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", "", false
	}
	rest, choice = rest[:i], rest[i+1:]
	j := strings.LastIndex(rest, ":")
	if j < 1 || j == len(rest)-1 || choice == "" {
		return "", "", "", false
	}
	return rest[:j], rest[j+1:], choice, true
}

// CardClosedText replaces a resolved card's buttons: the outcome and who
// chose it. Slack and Discord share the "<@id>" mention syntax.
func CardClosedText(outcome, userID string) string {
	return "› " + outcome + " — <@" + userID + ">"
}

// FormatAskCard renders an AskUserQuestion card as a chat message. bold is
// the platform's bold marker ("*" on Slack, "**" on Discord).
func FormatAskCard(data events.AskUserQuestionEventData, bold string) string {
	var sb strings.Builder
	for i, q := range data.Questions {
		if i > 0 {
			sb.WriteString("\n")
		}
		if q.Header != "" {
			fmt.Fprintf(&sb, "%s%s:%s ", bold, q.Header, bold)
		}
		sb.WriteString(q.Question)
		if q.MultiSelect {
			sb.WriteString(" (pick one or more)")
		}
		sb.WriteString("\n")
		for j, opt := range q.Options {
			fmt.Fprintf(&sb, "%d. %s", j+1, opt.Label)
			// Agents often repeat the label as the description.
			if opt.Description != "" && !strings.EqualFold(opt.Description, opt.Label) {
				sb.WriteString(" - " + opt.Description)
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")
	switch {
	case len(data.Questions) != 1:
		sb.WriteString(askReplyHintMulti)
	case data.Questions[0].MultiSelect:
		sb.WriteString(askReplyHintMultiSelect)
	default:
		sb.WriteString(askReplyHint)
	}
	return sb.String()
}

// FormatPlanCard renders an ExitPlanMode card as a chat message. bold is the
// platform's bold marker ("*" on Slack, "**" on Discord).
func FormatPlanCard(data events.ExitPlanModeEventData, bold string) string {
	return bold + "Plan ready for review" + bold + "\n\n" + data.Plan + "\n\n" + planReplyHint
}
