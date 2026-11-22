package web

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

func serveDashboard(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if strings.HasSuffix(name, ".gz") {
		http.NotFound(w, r)
		return
	}
	data, err := dashboardFiles.ReadFile(dashboardDir + "/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType := dashboardType(name); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if name != "index.html" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if compressed, err := dashboardFiles.ReadFile(dashboardDir + "/" + name + ".gz"); err == nil {
		w.Header().Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			data = compressed
			w.Header().Set("Content-Encoding", "gzip")
		}
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func acceptsGzip(values []string) bool {
	var wildcard bool
	for _, value := range values {
		for encoding := range strings.SplitSeq(value, ",") {
			name, parameters, _ := strings.Cut(encoding, ";")
			allowed := encodingAllowed(parameters)
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "gzip":
				return allowed
			case "*":
				wildcard = allowed
			}
		}
	}
	return wildcard
}

func encodingAllowed(parameters string) bool {
	for parameter := range strings.SplitSeq(parameters, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(parameter), "=")
		if strings.EqualFold(strings.TrimSpace(name), "q") {
			quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			return err == nil && quality > 0 && quality <= 1
		}
	}
	return true
}

// dashboardType fixes the types of scripts and fonts so they are the same on
// development hosts and in the container, which has no /etc/mime.types.
func dashboardType(name string) string {
	ext := path.Ext(name)
	switch ext {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	default:
		return mime.TypeByExtension(ext)
	}
}
