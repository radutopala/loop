//go:build !darwin && !linux

package daemon

import (
	"errors"
)

var errUnsupported = errors.New("daemon management is only supported on macOS (launchd) and Linux (systemd)")

func Start(_ System, _ string) error { return errUnsupported }
func Stop(_ System) error            { return errUnsupported }
func Status(_ System) (string, error) {
	return "", errUnsupported
}
