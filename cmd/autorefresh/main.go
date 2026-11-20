package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/config"
	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
)

// Release variables. goreleaser and the mise build task set them.
var (
	Version   = "0.0.0-dev"
	GitCommit = "unknown"
	Timestamp = "unknown"
)

var cli struct {
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

	// config
	file, err := waitForConfig(cli.Config, 5*time.Second)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed opening config")
	}
	cfg, err := config.Decode(file)
	file.Close()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed decoding config")
	}
	cacheRefresh, err := cron.ParseStandard(cfg.CacheRefresh)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed parsing cache-refresh")
	}

	ignoreReasons, err := store.ParseReasons(cfg.IgnoreReasons)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed parsing ignore-reasons")
	}

	// database
	st, err := store.Open(cli.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed opening database")
	}

	// refresher
	servers := make([]refresher.Server, 0, len(cfg.Plex))
	names := make([]string, 0, len(cfg.Plex))
	for _, s := range cfg.Plex {
		servers = append(servers, refresher.Server{
			Name:      s.Name,
			Plex:      plex.New(s.Name, s.URL, s.Token, s.RequestTimeout()),
			Libraries: s.Libraries,
		})
		names = append(names, s.Name)
	}

	r := refresher.New(servers, st, refresher.Options{
		MinimumAge:         cfg.MinimumAge,
		RefreshInterval:    cfg.RefreshInterval,
		MaxRefreshInterval: cfg.MaxRefreshInterval,
		Oversample:         cfg.Oversample,
		RefreshDelay:       cfg.RefreshDelay,
		CacheRefresh:       cacheRefresh,
		MaxRefreshesPerRun: cfg.MaxRefreshesPerRun,
		IgnoreReasons:      ignoreReasons,
		DryRun:             cli.DryRun,
	})

	log.Info().
		Str("version", version).
		Strs("servers", names).
		Str("schedule", cfg.Schedule).
		Bool("dry_run", cli.DryRun).
		Msg("Initialised")

	// SIGINT and SIGTERM cancel ctx. A run then stops before its next request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// With --once, each logged error gives exit code 1.
	if cli.Once {
		err := r.Run(ctx)
		if err != nil {
			log.Error().Err(err).Msg("Run failed")
		}
		if !closeStore(st) || err != nil {
			os.Exit(1)
		}
		return
	}

	// schedule. The first run waits for the schedule.
	run := &runner{run: r.Run}
	c := cron.New()
	if _, err := c.AddFunc(cfg.Schedule, func() { run.start(ctx) }); err != nil {
		closeStore(st)
		log.Fatal().Err(err).Msg("Failed adding schedule")
	}
	c.Start()

	<-ctx.Done()
	// A second signal now ends the process at once.
	stop()
	// Stop waits for a busy run to return.
	<-c.Stop().Done()
	closeStore(st)
	log.Info().Msg("Stopped")
}

// closeStore closes the database. If that fails, it logs an error and
// returns false.
func closeStore(st *store.Store) bool {
	if err := st.Close(); err != nil {
		log.Error().Err(err).Msg("Failed closing database")
		return false
	}
	return true
}
