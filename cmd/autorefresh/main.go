package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/config"
	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
	"github.com/l3uddz/autorefresh/internal/web"
)

// Release variables. goreleaser and the mise build task set them.
var (
	Version   = "0.0.0-dev"
	GitCommit = "unknown"
	Timestamp = "unknown"
)

// options holds the command line flags.
type options struct {
	Version kong.VersionFlag `help:"Print version information and quit"`

	// An empty path means a file in the default config directory.
	// applyDefaultPaths resolves it after parsing, so set paths never create
	// that directory.
	Config    string `type:"path" env:"AUTOREFRESH_CONFIG" help:"Configuration file path (default: config.yml in the default config directory)"`
	Database  string `type:"path" env:"AUTOREFRESH_DATABASE" help:"Database file path (default: autorefresh.db in the default config directory)"`
	Log       string `type:"path" env:"AUTOREFRESH_LOG" help:"Log file path (default: activity.log in the default config directory)"`
	Verbosity int    `type:"counter" default:"0" short:"v" env:"AUTOREFRESH_VERBOSITY" help:"Log level verbosity"`

	DryRun bool `env:"AUTOREFRESH_DRY_RUN" help:"Read Plex and update the cache, but send no refresh or match requests"`
	Once   bool `env:"AUTOREFRESH_ONCE" help:"Run one check at once and exit, instead of waiting for the schedule"`
}

var cli options

func main() {
	version := fmt.Sprintf("%s (%s@%s)", Version, GitCommit, Timestamp)

	// parse cli. kong.Parse exits if the arguments are not valid.
	kong.Parse(&cli,
		kong.Name("autorefresh"),
		kong.Description("Refresh Plex items that have missing metadata"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Summary: true,
			Compact: true,
		}),
		kong.Vars{
			"version": version,
		},
	)

	applyDefaultPaths(&cli.Config, &cli.Database, &cli.Log)
	log.Logger = newLogger(os.Stderr, cli.Log, cli.Verbosity)

	// SIGINT and SIGTERM cancel ctx. A run then stops before its next request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, stop, cli, version)
	stop()
	// run logged the error.
	if err != nil {
		os.Exit(1)
	}
}

// run loads the config, opens the database, and runs the refresher. With
// opts.Once, it runs one check. Otherwise it starts the UI if ui.listen is
// set, checks the tokens, starts the first run at once, then a run at each
// schedule time, until ctx ends. It then stops the UI, the schedule, and the
// database, in that order. stop ends the signal handling after the first
// signal, so a second signal ends the process at once. run logs each error
// that it returns. With opts.Once, a failed check or a failed close of the
// database returns an error, which gives exit code 1.
func run(ctx context.Context, stop context.CancelFunc, opts options, version string) error {
	// A setup error ends the process, so it logs at fatal level.
	fatal := func(err error, msg string) error {
		log.WithLevel(zerolog.FatalLevel).Err(err).Msg(msg)
		return err
	}

	// config
	data, err := readConfig(ctx, opts.Config, 5*time.Second)
	if ctx.Err() != nil {
		// A signal during the wait for the config stops the process.
		log.Info().Msg("Stopped")
		return nil
	}
	if err != nil {
		return fatal(err, "Failed reading config")
	}
	cfg, err := config.Decode(bytes.NewReader(data))
	if err != nil {
		return fatal(err, "Failed decoding config")
	}
	// database
	st, err := store.Open(opts.Database)
	if err != nil {
		return fatal(err, "Failed opening database")
	}
	if err := interruptRuns(st, opts, time.Now()); err != nil {
		closeStore(st)
		return fatal(err, "Failed marking interrupted runs")
	}

	// refresher
	servers, clients, names := newServers(cfg)

	// The interfaces stay nil without a token, so no TMDB request is sent.
	var lookup refresher.TMDB
	var tmdbCheck interface{ Check(context.Context) error }
	if cfg.TMDBToken != "" {
		tmdbClient := tmdb.New(cfg.TMDBToken, config.DefaultTimeout)
		lookup = tmdbClient
		tmdbCheck = tmdbClient
	}

	// The interfaces stay nil without the UI, so no nil *web.Status is a
	// non-nil interface.
	uiOn := uiEnabled(cfg, opts)
	var status *web.Status
	var progress refresher.Progress
	var rec healthRecorder
	if uiOn {
		status = web.NewStatus(names, cfg.TMDBToken != "")
		progress = status
		rec = status
	}

	rules := queueRules(cfg)
	r := refresher.New(servers, st, refresher.Options{
		MinimumAge:         rules.MinimumAge,
		RefreshInterval:    rules.RefreshInterval,
		MaxRefreshInterval: rules.MaxRefreshInterval,
		Oversample:         cfg.Oversample,
		RefreshDelay:       cfg.RefreshDelay,
		CacheRefresh:       cfg.CacheRefreshSchedule,
		MaxRefreshesPerRun: cfg.MaxRefreshesPerRun,
		IgnoreReasons:      rules.Ignored,
		DryRun:             opts.DryRun,
		HistoryRetention:   cfg.HistoryRetention,
		TMDB:               lookup,
		Progress:           progress,
	})

	log.Info().
		Str("version", version).
		Strs("servers", names).
		Str("schedule", cfg.Schedule).
		Bool("dry_run", opts.DryRun).
		Bool("tmdb", lookup != nil).
		Msg("Initialised")

	// After the first signal, a second signal ends the process at once.
	defer context.AfterFunc(ctx, stop)()

	if opts.Once {
		return runOnce(ctx, r, st)
	}

	// The UI binds before the token checks, so a bind error stops the start
	// before any request to Plex.
	var ui *web.Server
	if uiOn {
		ui = newUIServer(cfg, rules, st, status, clients)
		if err := startUI(cfg.UI, ui); err != nil {
			closeStore(st)
			return fatal(err, "Failed starting UI listener")
		}
	}

	// schedule. A check of each server and TMDB shows a wrong token or URL at
	// once, before the first run.
	checkTokens(ctx, servers, tmdbCheck, rec)
	serveUntilStopped(ctx, ui, refresher.NewJobs(r), cfg.RunSchedule)
	closeStore(st)
	log.Info().Msg("Stopped")
	return nil
}

// queueRules returns the rules of cfg that decide the queue state of an
// item. The refresher and the dashboard both get them, so the schedule and
// the dashboard queue cannot disagree.
func queueRules(cfg config.Config) store.DueRules {
	return store.DueRules{
		MinimumAge:         cfg.MinimumAge,
		RefreshInterval:    cfg.RefreshInterval,
		MaxRefreshInterval: cfg.MaxRefreshInterval,
		Ignored:            cfg.Ignored,
	}
}

// newServers returns the refresher server, the Plex client, and the name of
// each configured Plex server, in the configured order.
func newServers(cfg config.Config) ([]refresher.Server, []*plex.Client, []string) {
	servers := make([]refresher.Server, 0, len(cfg.Plex))
	clients := make([]*plex.Client, 0, len(cfg.Plex))
	names := make([]string, 0, len(cfg.Plex))
	for _, s := range cfg.Plex {
		client := plex.New(s.Name, s.BaseURL, s.Token, s.RequestTimeout())
		servers = append(servers, refresher.Server{
			Name:      s.Name,
			Plex:      client,
			Libraries: s.Libraries,
		})
		clients = append(clients, client)
		names = append(names, s.Name)
	}
	return servers, clients, names
}

// runOnce runs r once and closes the database. It returns the error of the
// run, or else the error of the close.
func runOnce(ctx context.Context, r *refresher.Refresher, st *store.Store) error {
	err := r.Run(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Run failed")
	}
	if closeErr := closeStore(st); err == nil {
		err = closeErr
	}
	return err
}

// interruptRuns marks the runs that a stopped process left as running, so a
// scheduled start finds none. A process with --once or --dry-run may run
// beside a scheduled process, so it leaves them.
func interruptRuns(st *store.Store, opts options, now time.Time) error {
	if opts.Once || opts.DryRun {
		return nil
	}
	runs, err := st.InterruptRuns(now)
	if err != nil {
		return err
	}
	if runs > 0 {
		log.Info().Int("runs", runs).Msg("Marked interrupted runs")
	}
	return nil
}

// closeStore closes the database. If that fails, it logs and returns the
// error.
func closeStore(st *store.Store) error {
	if err := st.Close(); err != nil {
		log.Error().Err(err).Msg("Failed closing database")
		return err
	}
	return nil
}
