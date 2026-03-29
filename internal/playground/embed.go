package playground

import "embed"

// Examples contains the embedded example playground files.
//
//go:embed examples
var Examples embed.FS
