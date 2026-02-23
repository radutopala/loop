package config

import "embed"

//go:embed config.global.example.json
var ExampleConfig []byte

//go:embed config.project.example.json
var ProjectExampleConfig []byte

//go:embed slack.manifest.json
var SlackManifest []byte

//go:embed templates
var Templates embed.FS
