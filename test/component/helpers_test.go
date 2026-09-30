//go:build component

package component

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/apiauth"
)

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// apiToken reads the test daemon's owner API token (LOOP_API_TOKEN_FILE),
// fresh on each call so a rotation during a scenario is picked up.
func apiToken() string {
	b, err := os.ReadFile(os.Getenv("LOOP_API_TOKEN_FILE"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// newAPIClient returns an HTTP client that authenticates as the owner.
func newAPIClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &apiauth.Transport{Token: apiToken}}
}
