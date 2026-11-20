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
	Plex               []Server      `yaml:"plex"`
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

// Decode reads YAML from r over the defaults, rejects unknown keys, and
// validates the result.
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

func (c Config) validate() error {
	if err := validateCron("schedule", c.Schedule); err != nil {
		return err
	}
	if err := validateCron("cache-refresh", c.CacheRefresh); err != nil {
		return err
	}
	if c.RefreshInterval <= 0 {
		return errors.New("refresh-interval must be greater than zero")
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
	if _, err := store.ParseReasons(c.IgnoreReasons); err != nil {
		return fmt.Errorf("ignore-reasons: %w", err)
	}
	if len(c.Plex) == 0 {
		return errors.New("no plex server is configured")
	}

	names := make(map[string]struct{}, len(c.Plex))
	for i, s := range c.Plex {
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

// validateCron checks that expr is a standard cron expression that fires.
// For an expression that never fires, such as February 30, Next returns the
// zero time.
func validateCron(key, expr string) error {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return fmt.Errorf("%s %q: %w", key, expr, err)
	}
	if sched.Next(time.Now()).IsZero() {
		return fmt.Errorf("%s %q: the expression never fires", key, expr)
	}
	return nil
}

// label names a server in errors. It never uses the URL or the token.
func (s Server) label(index int) string {
	if s.Name == "" {
		return fmt.Sprintf("#%d", index+1)
	}
	return fmt.Sprintf("%q", s.Name)
}

func (s Server) validate() error {
	if s.Name == "" {
		return errors.New("name is empty")
	}
	if s.URL == "" {
		return errors.New("url is empty")
	}
	// The error of url.Parse contains the URL, so it is not wrapped.
	u, err := url.Parse(s.URL)
	if err != nil {
		return errors.New("url is not valid")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must use http or https")
	}
	if u.Hostname() == "" {
		return errors.New("url has no host")
	}
	if s.Token == "" {
		return errors.New("token is empty")
	}
	if s.RequestTimeout() <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	for _, lib := range s.Libraries {
		if lib == "" {
			return errors.New("a library title is empty")
		}
	}
	return nil
}
