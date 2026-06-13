// Package web embeds the static demo UI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var static embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // embedded FS layout is fixed at compile time
	}
	return http.FileServer(http.FS(sub))
}
