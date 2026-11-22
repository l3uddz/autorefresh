package web

import "testing"

func TestDashboardTypeNamesFonts(t *testing.T) {
	for name, want := range map[string]string{
		"assets/figtree-latin-400-normal.woff2": "font/woff2",
		"assets/index.js":                       "text/javascript; charset=utf-8",
		"index.html":                            "text/html; charset=utf-8",
	} {
		if got := dashboardType(name); got != want {
			t.Errorf("dashboardType(%q) = %q, want %q", name, got, want)
		}
	}
}
