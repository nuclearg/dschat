package web

import "embed"

// FS holds the built official frontend.
//
//go:embed all:dist
var FS embed.FS
