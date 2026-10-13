// Package ui embeds the built web panel (web/dist, copied here at build time
// by `make panel` or the release workflow).
package ui

import (
	"embed"
	"io/fs"
	"testing/fstest"
)

//go:embed all:dist
var files embed.FS

// notBuilt is served when the binary was compiled without the web UI, so the
// panel explains what happened instead of answering 404.
const notBuilt = `<!doctype html><meta charset="utf-8"><title>Ike</title>
<body style="font:16px system-ui;max-width:36rem;margin:15vh auto;padding:0 1rem">
<h1>The web panel wasn't built into this binary</h1>
<p>Build it with <code>make panel</code>, or install a release binary.
The API at <code>/api/v1</code> still works.</p></body>`

// FS is the web panel's static files.
func FS() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err == nil {
		if _, err = fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	return fstest.MapFS{"index.html": {Data: []byte(notBuilt)}}
}
