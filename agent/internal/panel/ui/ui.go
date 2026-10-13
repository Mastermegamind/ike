// Package ui embeds the built web panel (web/dist, copied here at build time).
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

// FS is the web panel's static files.
func FS() fs.FS {
	sub, _ := fs.Sub(files, "dist")
	return sub
}
