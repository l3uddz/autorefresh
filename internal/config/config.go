// Package config loads and validates the autorefresh configuration.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"go.yaml.in/yaml/v3"

	"github.com/l3uddz/autorefresh/internal/store"
)

// Config is the autorefresh configuration.
type Config struct {
	Schedule           string        `yaml:"schedule"`
	MinimumAge         time.Duration `yaml:"minimum-age"`
	RefreshInterval    time.Duration `yaml:"refresh-interval"`
	MaxRefreshInterval time.Duration `yaml:"max-refresh-interval"`
	CacheRefresh       string        `yaml:"cache-refresh"` // cron expression
	Oversample         time.Duration `yaml:"oversample"`
	RefreshDelay       time.Duration `yaml:"refresh-delay"`
	MaxRefreshesPerRun int           `yaml:"max-refreshes-per-run"` // 0: no limit
	IgnoreReasons      []string      `yaml:"ignore-reasons"`        // reason names such as "no-art"
	TMDBToken          string        `yaml:"tmdb-token"`            // "": no TMDB fallback in fix match
	Plex               []Server      `yaml:"plex"`

	// Decode parses these values from the keys above.
	RunSchedule          cron.Schedule `yaml:"-"` // Schedule
	CacheRefreshSchedule cron.Schedule `yaml:"-"` // CacheRefresh
	Ignored              store.Reasons `yaml:"-"` // IgnoreReasons
}

// DefaultTimeout limits each request to a server without a timeout key.
const DefaultTimeout = 30 * time.Second

// Server is one Plex server.
type Server struct {
	Name      string         `yaml:"name"`
	URL       string         `yaml:"url"`
	Token     string         `yaml:"token"`
	Libraries []string       `yaml:"libraries"` // empty: all supported libraries
	Timeout   *time.Duration `yaml:"timeout"`   // nil: DefaultTimeout

	BaseURL *url.URL `yaml:"-"` // URL, parsed by Decode
}

// RequestTimeout returns the limit of each request to the server.
func (s Server) RequestTimeout() time.Duration {
	if s.Timeout == nil {
		return DefaultTimeout
	}
	return *s.Timeout
}

func defaults() Config {
	return Config{
		Schedule:           "0 * * * *",
		MinimumAge:         24 * time.Hour,
		RefreshInterval:    24 * time.Hour,
		MaxRefreshInterval: 7 * 24 * time.Hour,
		CacheRefresh:       "0 3 * * 0",
		Oversample:         48 * time.Hour,
		RefreshDelay:       5 * time.Second,
		MaxRefreshesPerRun: 250,
	}
}

// Decode reads YAML from r over the defaults, rejects unknown keys,
// validates the result, and sets the parsed values.
func Decode(r io.Reader) (Config, error) {
	cfg := defaults()

	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	// An empty document returns io.EOF. Keep the defaults and validate them.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, yamlError(err)
	}
	// Only one document is allowed. A later one would be ignored silently.
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return Config{}, errors.New("configuration has more than one YAML document")
	case !errors.Is(err, io.EOF):
		return Config{}, yamlError(err)
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

var (
	typeErrorLine   = regexp.MustCompile(`^line (\d+):`)
	syntaxErrorLine = regexp.MustCompile(`^yaml: line (\d+):`)
)

// yamlError keeps only the line numbers of a YAML error. The yaml package
// puts values such as the token into its messages, so the error is not
// wrapped. Line numbers come only from the fixed prefix of each message,
// because a value after the prefix could imitate a line number.
func yamlError(err error) error {
	var messages []string
	re := syntaxErrorLine
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		messages = typeErr.Errors
		re = typeErrorLine
	} else {
		messages = []string{err.Error()}
	}

	var lines []string
	seen := map[string]bool{}
	for _, msg := range messages {
		if m := re.FindStringSubmatch(msg); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			lines = append(lines, m[1])
		}
	}
	switch len(lines) {
	case 0:
		return errors.New("decode configuration: invalid YAML")
	case 1:
		return fmt.Errorf("decode configuration: invalid YAML at line %s", lines[0])
	}
	return fmt.Errorf("decode configuration: invalid YAML at lines %s", strings.Join(lines, ", "))
}

// validate checks the configuration and sets the parsed values.
func (c *Config) validate() error {
	var err error
	if c.RunSchedule, err = parseCron("schedule", c.Schedule); err != nil {
		return err
	}
	if c.CacheRefreshSchedule, err = parseCron("cache-refresh", c.CacheRefresh); err != nil {
		return err
	}
	if c.RefreshInterval < time.Millisecond {
		return errors.New("refresh-interval must be at least 1ms")
	}
	if c.MaxRefreshInterval < 0 {
		return errors.New("max-refresh-interval must not be negative")
	}
	if c.MinimumAge < 0 {
		return errors.New("minimum-age must not be negative")
	}
	if c.Oversample < 0 {
		return errors.New("oversample must not be negative")
	}
	if c.RefreshDelay < 0 {
		return errors.New("refresh-delay must not be negative")
	}
	if c.MaxRefreshesPerRun < 0 {
		return errors.New("max-refreshes-per-run must not be negative")
	}
	if c.Ignored, err = store.ParseReasons(c.IgnoreReasons); err != nil {
		return fmt.Errorf("ignore-reasons: %w", err)
	}
	if c.TMDBToken != "" && hasControlOrSpace(c.TMDBToken) {
		return errors.New("tmdb-token must not contain whitespace or control characters")
	}
	return c.validateServers()
}

// validateServers validates each configured Plex server and ensures unique
// server names.
func (c *Config) validateServers() error {
	if len(c.Plex) == 0 {
		return errors.New("no plex server is configured")
	}
	names := make(map[string]struct{}, len(c.Plex))
	for i := range c.Plex {
		s := &c.Plex[i]
		if err := s.validate(); err != nil {
			return fmt.Errorf("plex server %s: %w", s.label(i), err)
		}
		if _, dup := names[s.Name]; dup {
			return fmt.Errorf("plex server %s: name is used more than once", s.label(i))
		}
		names[s.Name] = struct{}{}
	}
	return nil
}

// hasControl reports whether s contains any ASCII control character.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// hasControlOrSpace reports whether s contains any ASCII control character or
// whitespace, which cannot appear in a valid Plex or TMDB header token.
func hasControlOrSpace(s string) bool {
	for _, r := range s {
		if r <= 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// parseCron parses expr as a standard cron expression that fires. For an
// expression that never fires, such as February 30, Next returns the zero
// time.
func parseCron(key, expr string) (cron.Schedule, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", key, expr, err)
	}
	if sched.Next(time.Now()).IsZero() {
		return nil, fmt.Errorf("%s %q: the expression never fires", key, expr)
	}
	return sched, nil
}

// label names a server in errors. It never uses the URL or the token.
func (s Server) label(index int) string {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Sprintf("#%d", index+1)
	}
	return fmt.Sprintf("%q", s.Name)
}

// validate checks the server and sets BaseURL.
func (s *Server) validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is empty")
	}
	if hasControl(s.Name) {
		return errors.New("name must not contain control characters")
	}
	u, err := validateURL(s.URL)
	if err != nil {
		return err
	}
	s.BaseURL = u
	if s.Token == "" {
		return errors.New("token is empty")
	}
	if hasControlOrSpace(s.Token) {
		return errors.New("token must not contain whitespace or control characters")
	}
	if s.RequestTimeout() <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	for _, lib := range s.Libraries {
		if strings.TrimSpace(lib) == "" {
			return errors.New("a library title is empty")
		}
		if hasControl(lib) {
			return errors.New("a library title must not contain control characters")
		}
	}
	return nil
}

// validateURL parses and validates a Plex server URL without leaking the URL
// in error messages.
func validateURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("url is empty")
	}
	// The error of url.Parse contains the URL, so it is not wrapped.
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("url is not valid")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("url must use http or https")
	}
	if u.Hostname() == "" {
		return nil, errors.New("url has no host")
	}
	if u.User != nil {
		return nil, errors.New("url must not have userinfo")
	}
	// The requests replace the query and drop the fragment, so either one
	// is a silent mistake.
	if u.RawQuery != "" || u.ForceQuery {
		return nil, errors.New("url must not have a query")
	}
	if strings.Contains(raw, "#") {
		return nil, errors.New("url must not have a fragment")
	}
	return u, nil
}
