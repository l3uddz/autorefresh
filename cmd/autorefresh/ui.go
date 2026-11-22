package main

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/config"
	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/web"
)

// listenFunc binds the UI listener. Tests replace it, like sleepFunc and
// readFileFunc.
var listenFunc = net.Listen

// uiStopTimeout bounds the graceful stop of the UI. Stop can then wait up
// to its forced-close wait more.
const uiStopTimeout = 5 * time.Second

// uiEnabled reports whether the UI runs: ui.listen is set, and neither
// --once nor --dry-run is set. A dry run can run beside the service, so it
// must not take the port of the service.
func uiEnabled(cfg config.Config, opts options) bool {
	if cfg.UI.Listen == "" {
		return false
	}
	if opts.Once || opts.DryRun {
		log.Info().Msg("UI is off with --once or --dry-run")
		return false
	}
	return true
}

// newUIServer returns the dashboard server of cfg with the queue rules.
// clients holds the Plex client of each server in cfg.Plex, in the same
// order.
func newUIServer(cfg config.Config, rules store.DueRules, st *store.Store, status *web.Status, clients []*plex.Client, actions web.Actions, jobs web.Jobs) *web.Server {
	servers := make([]web.PlexServer, len(clients))
	for i, client := range clients {
		servers[i] = web.PlexServer{Name: cfg.Plex[i].Name, Plex: client}
	}
	credentials := uiCredentials(cfg)
	return web.New(web.Options{
		Actions:     actions,
		Jobs:        jobs,
		Username:    cfg.UI.Username,
		Password:    cfg.UI.Password,
		Status:      status,
		Servers:     servers,
		Store:       st,
		Rules:       rules,
		Secrets:     uiSecrets(cfg, credentials, clients),
		Credentials: credentials,
	})
}

// uiCredentials returns the credentials of cfg: the token of each Plex
// server, the TMDB token, and the UI password. The API drops a candidate
// poster whose URL holds one.
func uiCredentials(cfg config.Config) []string {
	creds := make([]string, 0, len(cfg.Plex)+2)
	for _, s := range cfg.Plex {
		creds = append(creds, s.Token)
	}
	return append(creds, cfg.TMDBToken, cfg.UI.Password)
}

// uiSecrets returns the values that the API redacts from stored error
// texts: the credentials, and the URL host and the path prefix of each
// Plex server.
func uiSecrets(cfg config.Config, credentials []string, clients []*plex.Client) []string {
	secrets := make([]string, 0, len(credentials)+2*len(clients))
	secrets = append(secrets, credentials...)
	for i, client := range clients {
		secrets = append(secrets, cfg.Plex[i].BaseURL.Host, client.PathPrefix())
	}
	return secrets
}

// startUI binds the listen address of cfg and serves ui on it.
func startUI(cfg config.UI, ui *web.Server) error {
	ln, err := listenFunc("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	ui.Start(ln)
	log.Info().Str("listen", ln.Addr().String()).Bool("auth", cfg.Username != "").Msg("UI listening")
	if cfg.Username == "" {
		log.Warn().Msg("UI has no authentication, so allow access only through an authenticating reverse proxy or a trusted network")
	}
	return nil
}

// stopUI stops ui, if there is one. It logs a failed graceful stop.
func stopUI(ui uiStopper) {
	if ui == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiStopTimeout)
	defer cancel()
	if err := ui.Stop(ctx); err != nil {
		log.Warn().Err(err).Msg("Stopping UI failed")
	}
}

type uiStopper interface{ Stop(context.Context) error }

type scheduledJobs interface {
	scheduledStarter
	Close()
}

// serveUntilStopped runs the schedule until ctx ends. If ctx already ended,
// for example during the token checks, the schedule never starts. Then it
// stops in this order: the UI, so that no request still reads the store,
// then the schedule and the busy run. The schedule has its own context, so
// a busy run goes on while the UI stops. ui and onNext can be nil; serve
// gives onNext the next time of the schedule.
func serveUntilStopped(ctx context.Context, ui uiStopper, r scheduledJobs, sched cron.Schedule, onNext func(time.Time)) {
	schedCtx, cancelSched := context.WithCancel(context.Background())
	defer cancelSched()
	var served sync.WaitGroup
	if ctx.Err() == nil {
		served.Go(func() { serve(schedCtx, r, sched, onNext) })
	}
	<-ctx.Done()
	stopUI(ui)
	cancelSched()
	served.Wait()
	r.Close()
}
