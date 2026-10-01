//go:build windows

package daemon

import "errors"

var errDetachedUnsupported = errors.New("detached daemon is not supported on Windows")

func (RealSystem) StartDetached(string, []string, string) (int, error) {
	return 0, errDetachedUnsupported
}

func (RealSystem) Terminate(int) error { return errDetachedUnsupported }
