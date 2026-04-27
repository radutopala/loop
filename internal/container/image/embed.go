package image

import (
	"embed"
	"fmt"
)

//go:embed Dockerfile chrome.Dockerfile chrome-entrypoint.sh entrypoint.sh setup.sh agent-bashrc
var FS embed.FS

// MustRead returns the embedded file with the given name. It panics if the
// file is not in the embedded FS — used by callers (e.g. fsmigrate) that
// only ever look up filenames known at compile time, so a missing file
// indicates a build-time error rather than a runtime condition.
func MustRead(name string) []byte {
	data, err := FS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("containerimage: missing embedded file %q: %v", name, err))
	}
	return data
}

//go:embed Dockerfile
var Dockerfile []byte

//go:embed chrome.Dockerfile
var ChromeDockerfile []byte

//go:embed chrome-entrypoint.sh
var ChromeEntrypoint []byte

//go:embed entrypoint.sh
var Entrypoint []byte

//go:embed setup.sh
var Setup []byte

//go:embed agent-bashrc
var AgentBashrc []byte
