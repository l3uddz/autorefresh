package config

import (
	"strings"
	"testing"
	"time"
)

const minimalServer = `
plex:
  - name: main
    url: http://plex.example.invalid:32400
    token: example-token
`

func TestDecodeAppliesDefaults(t *testing.T) {
	cfg, err := Decode(strings.NewReader(minimalServer))
	if err != nil {
		t.Fatalf("Decode returned an error: %v", err)
	}
	if cfg.Schedule != "0 * * * *" {
		t.Errorf("Schedule = %q", cfg.Schedule)
	}
	if cfg.MinimumAge != 24*time.Hour {
		t.Errorf("MinimumAge = %v", cfg.MinimumAge)
	}
	if cfg.RefreshInterval != 12*time.Hour {
		t.Errorf("RefreshInterval = %v", cfg.RefreshInterval)
	}
	if cfg.MaxRefreshInterval != 168*time.Hour {
		t.Errorf("MaxRefreshInterval = %v", cfg.MaxRefreshInterval)
	}
	if cfg.CacheRefresh != "0 3 * * 0" {
		t.Errorf("CacheRefresh = %q", cfg.CacheRefresh)
	}
	if cfg.Oversample != 48*time.Hour {
		t.Errorf("Oversample = %v", cfg.Oversample)
	}
	if cfg.RefreshDelay != 5*time.Second {
		t.Errorf("RefreshDelay = %v", cfg.RefreshDelay)
	}
	if cfg.MaxRefreshesPerRun != 250 {
		t.Errorf("MaxRefreshesPerRun = %d, want 250", cfg.MaxRefreshesPerRun)
	}
}

func TestDecodeAcceptsZeroMaxRefreshesAsNoLimit(t *testing.T) {
	cfg, err := Decode(strings.NewReader("max-refreshes-per-run: 0\n" + minimalServer))
	if err != nil {
		t.Fatalf("Decode returned an error: %v", err)
	}
	if cfg.MaxRefreshesPerRun != 0 {
		t.Errorf("MaxRefreshesPerRun = %d, want 0", cfg.MaxRefreshesPerRun)
	}
}

func TestDecodeReadsAllKeys(t *testing.T) {
	const doc = `
schedule: "30 * * * *"
minimum-age: 36h
refresh-interval: 6h
max-refresh-interval: 72h
cache-refresh: "0 4 * * 1"
oversample: 24h
refresh-delay: 2s
max-refreshes-per-run: 100
plex:
  - name: main
    url: http://plex.example.invalid:32400
    token: example-token
    libraries:
      - Movies
      - TV
`
	cfg, err := Decode(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Decode returned an error: %v", err)
	}
	if cfg.Schedule != "30 * * * *" || cfg.CacheRefresh != "0 4 * * 1" {
		t.Errorf("cron values = %q, %q", cfg.Schedule, cfg.CacheRefresh)
	}
	if cfg.MinimumAge != 36*time.Hour || cfg.RefreshInterval != 6*time.Hour {
		t.Errorf("ages = %v, %v", cfg.MinimumAge, cfg.RefreshInterval)
	}
	if cfg.MaxRefreshInterval != 72*time.Hour {
		t.Errorf("MaxRefreshInterval = %v", cfg.MaxRefreshInterval)
	}
	if cfg.Oversample != 24*time.Hour || cfg.RefreshDelay != 2*time.Second {
		t.Errorf("oversample, delay = %v, %v", cfg.Oversample, cfg.RefreshDelay)
	}
	if cfg.MaxRefreshesPerRun != 100 {
		t.Errorf("MaxRefreshesPerRun = %d, want 100", cfg.MaxRefreshesPerRun)
	}
	if len(cfg.Plex) != 1 {
		t.Fatalf("len(Plex) = %d, want 1", len(cfg.Plex))
	}
	s := cfg.Plex[0]
	if s.Name != "main" || s.URL != "http://plex.example.invalid:32400" || s.Token != "example-token" {
		t.Errorf("server = %+v", s)
	}
	if len(s.Libraries) != 2 || s.Libraries[0] != "Movies" || s.Libraries[1] != "TV" {
		t.Errorf("Libraries = %v", s.Libraries)
	}
}

func TestDecodeRejectsUnknownKey(t *testing.T) {
	_, err := Decode(strings.NewReader("refresh-dely: 5s\n" + minimalServer))
	if err == nil {
		t.Fatal("Decode accepted an unknown key")
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error %q does not name the line", err)
	}
}

func TestDecodeRejectsInvalidConfig(t *testing.T) {
	const server = "    url: http://plex.example.invalid:32400\n    token: example-token\n"
	tests := []struct {
		name string
		doc  string
	}{
		{"empty document", ""},
		{"second document", minimalServer + "---\nrefresh-dely: 1s\n"},
		{"no plex entry", "schedule: \"0 * * * *\"\n"},
		{"duplicate server names", "plex:\n  - name: a\n" + server + "  - name: a\n" + server},
		{"empty name", "plex:\n  - name: \"\"\n" + server},
		{"empty url", "plex:\n  - name: a\n    url: \"\"\n    token: t\n"},
		{"unsupported scheme", "plex:\n  - name: a\n    url: ftp://plex.example.invalid\n    token: t\n"},
		{"url without host", "plex:\n  - name: a\n    url: http://\n    token: t\n"},
		{"url with port but no host", "plex:\n  - name: a\n    url: \"http://:32400\"\n    token: t\n"},
		{"empty token", "plex:\n  - name: a\n    url: http://plex.example.invalid\n    token: \"\"\n"},
		{"invalid schedule", "schedule: every hour\n" + minimalServer},
		{"zero refresh interval", "refresh-interval: 0s\n" + minimalServer},
		{"negative max refresh interval", "max-refresh-interval: -1h\n" + minimalServer},
		{"cache refresh not cron", "cache-refresh: 168h\n" + minimalServer},
		{"schedule never fires", "schedule: \"0 0 30 2 *\"\n" + minimalServer},
		{"cache refresh never fires", "cache-refresh: \"0 0 30 2 *\"\n" + minimalServer},
		{"negative minimum age", "minimum-age: -1h\n" + minimalServer},
		{"negative oversample", "oversample: -1h\n" + minimalServer},
		{"negative refresh delay", "refresh-delay: -1s\n" + minimalServer},
		{"negative max refreshes", "max-refreshes-per-run: -1\n" + minimalServer},
		{"empty library title", "plex:\n  - name: a\n" + server + "    libraries: [\"\"]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(tt.doc)); err == nil {
				t.Fatal("Decode accepted an invalid configuration")
			}
		})
	}
}

func TestDecodeErrorsOmitSecrets(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		token    string
		wantName bool
	}{
		{"unsupported scheme", "ftp://secret-host.example.invalid", "secret-token", true},
		{"url parse failure", "http://secret-host.example.invalid:bad", "secret-token", true},
		{"yaml type error in token", "http://plex.example.invalid", "!!bool secret-token", false},
		{"yaml type error with line-like value", "http://plex.example.invalid", "!!bool line 739582", false},
		{"yaml type error in url", "!!int http://secret-host.example.invalid", "secret-token", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := "plex:\n  - name: main\n    url: " + quoteUnlessTagged(tt.url) + "\n    token: " + tt.token + "\n"
			_, err := Decode(strings.NewReader(doc))
			if err == nil {
				t.Fatal("Decode accepted an invalid configuration")
			}
			msg := err.Error()
			if tt.wantName && !strings.Contains(msg, "main") {
				t.Errorf("error %q does not name the server", msg)
			}
			for _, secret := range []string{"secret-host", "secret-token", "739582"} {
				if strings.Contains(msg, secret) {
					t.Errorf("error %q contains %q", msg, secret)
				}
			}
		})
	}
}

func quoteUnlessTagged(v string) string {
	if strings.HasPrefix(v, "!!") {
		return v
	}
	return `"` + v + `"`
}

func TestDecodeSyntaxErrorNamesLine(t *testing.T) {
	_, err := Decode(strings.NewReader("schedule: \"0 * * * *\"\nplex: [\n"))
	if err == nil {
		t.Fatal("Decode accepted a syntax error")
	}
	if !strings.Contains(err.Error(), "line ") {
		t.Errorf("error %q does not name the line", err)
	}
}
