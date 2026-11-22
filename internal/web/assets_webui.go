//go:build webui

package web

import "embed"

//go:embed dist
var dashboardFiles embed.FS

const dashboardDir = "dist"
