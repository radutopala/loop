package types

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// MaxTicketURLLen caps a ticket URL, in bytes.
const MaxTicketURLLen = 2048

// NormalizeTicketURL trims a ticket URL and checks it's an absolute http(s)
// link; any tracker's (Jira, GitHub, Linear, …) will do. Empty is allowed and
// clears it.
func NormalizeTicketURL(raw string) (string, error) {
	ticketURL := strings.TrimSpace(raw)
	if ticketURL == "" {
		return "", nil
	}
	if len(ticketURL) > MaxTicketURLLen {
		return "", fmt.Errorf("ticket_url is longer than %d characters", MaxTicketURLLen)
	}
	u, err := url.Parse(ticketURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("ticket_url must be an absolute http(s) URL")
	}
	return ticketURL, nil
}
