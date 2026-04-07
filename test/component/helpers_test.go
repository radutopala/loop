//go:build component

package component

import "os"

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
