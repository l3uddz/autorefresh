//go:build !webui

package web

import "embed"

//go:embed fallback
var dashboardFiles embed.FS

const dashboardDir = "fallback"
