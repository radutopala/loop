package image

import (
	_ "embed"
)

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
