package readme

import (
	_ "embed"
)

//go:generate cp ../../README.md README.md

//go:embed README.md
var Content string
