package main

import (
	"embed"
	"io/fs"
)

//go:embed all:dashboard/dist
var dashboardEmbed embed.FS

// dashboardFS returns the embedded dashboard filesystem, or nil if empty.
// The dashboard must be built before compiling: cd dashboard && npm run build
// Then copy to cmd/micelio/dashboard/dist/ or symlink it.
func dashboardFS() fs.FS {
	// Check for index.html — the definitive marker of a built dashboard
	if _, err := fs.Stat(dashboardEmbed, "dashboard/dist/index.html"); err != nil {
		return nil
	}
	sub, err := fs.Sub(dashboardEmbed, "dashboard/dist")
	if err != nil {
		return nil
	}
	return sub
}
