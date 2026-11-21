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
// opts.Once, it runs one check. Otherwise it checks the tokens, starts the
// first run at once, then a run at each schedule time, until ctx ends. stop
// ends the signal handling after the first signal, so a second signal ends
// the process at once. run logs each error that it returns. With opts.Once,
// a failed check or a failed close of the database returns an error, which
// gives exit code 1.
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

	// refresher
	servers := make([]refresher.Server, 0, len(cfg.Plex))
	names := make([]string, 0, len(cfg.Plex))
	for _, s := range cfg.Plex {
		servers = append(servers, refresher.Server{
			Name:      s.Name,
			Plex:      plex.New(s.Name, s.BaseURL, s.Token, s.RequestTimeout()),
			Libraries: s.Libraries,
		})
		names = append(names, s.Name)
	}

	// The interface stays nil without a token, so the refresher sends no
	// TMDB requests.
	var tmdbClient *tmdb.Client
	var lookup refresher.TMDB
	if cfg.TMDBToken != "" {
		tmdbClient = tmdb.New(cfg.TMDBToken, config.DefaultTimeout)
		lookup = tmdbClient
	}

	r := refresher.New(servers, st, refresher.Options{
		MinimumAge:         cfg.MinimumAge,
		RefreshInterval:    cfg.RefreshInterval,
		MaxRefreshInterval: cfg.MaxRefreshInterval,
		Oversample:         cfg.Oversample,
		RefreshDelay:       cfg.RefreshDelay,
		CacheRefresh:       cfg.CacheRefreshSchedule,
		MaxRefreshesPerRun: cfg.MaxRefreshesPerRun,
		IgnoreReasons:      cfg.Ignored,
		DryRun:             opts.DryRun,
		TMDB:               lookup,
	})

	log.Info().
		Str("version", version).
		Strs("servers", names).
		Str("schedule", cfg.Schedule).
		Bool("dry_run", opts.DryRun).
		Bool("tmdb", tmdbClient != nil).
		Msg("Initialised")

	if opts.Once {
		err := r.Run(ctx)
		if err != nil {
			log.Error().Err(err).Msg("Run failed")
		}
		if closeErr := closeStore(st); err == nil {
			err = closeErr
		}
		return err
	}

	// schedule. A check of each server shows a wrong token or URL at once,
	// before the first run.
	checkServers(ctx, servers)
	if tmdbClient != nil {
		checkTMDB(ctx, tmdbClient)
	}
	// After the first signal, a second signal ends the process at once.
	context.AfterFunc(ctx, stop)
	serve(ctx, &runner{run: r.Run}, cfg.RunSchedule)
	closeStore(st)
	log.Info().Msg("Stopped")
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
