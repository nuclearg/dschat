package web

import "embed"

// FS holds the built official frontend (dist) and the legacy sync UI (legacy).
//
//go:embed all:dist all:legacy
var FS embed.FS
