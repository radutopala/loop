package readme

import (
	"embed"
	"encoding/base64"
	"io/fs"
	"mime"
	"path"
	"regexp"
	"sync"
)

//go:generate cp ../../README.md README.md
//go:generate mkdir -p docs/static/images
//go:generate cp ../../docs/static/images/architecture.svg docs/static/images/architecture.svg

//go:embed README.md
var Content string

// images are the images the README shows, at the paths it gives them.
//
//go:embed docs/static/images
var images embed.FS

// imageSrc matches an image's relative src attribute.
var imageSrc = regexp.MustCompile(`src="([^":]+)"`)

// Inlined returns Content with each image it ships with as a data: URI, for
// a page that renders it without the repository at hand.
var Inlined = sync.OnceValue(func() string { return inline(Content, images) })

// inline replaces the src of each image of md found in files with a data:
// URI of it.
func inline(md string, files fs.FS) string {
	return imageSrc.ReplaceAllStringFunc(md, func(attr string) string {
		name := imageSrc.FindStringSubmatch(attr)[1]
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return attr
		}
		return `src="data:` + mime.TypeByExtension(path.Ext(name)) + ";base64," + base64.StdEncoding.EncodeToString(data) + `"`
	})
}
