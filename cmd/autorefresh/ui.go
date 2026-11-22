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

// newUIServer returns the dashboard server of cfg. clients holds the Plex
// client of each server in cfg.Plex, in the same order.
func newUIServer(cfg config.Config, st *store.Store, status *web.Status, clients []*plex.Client) *web.Server {
	servers := make([]web.PlexServer, len(clients))
	for i, client := range clients {
		servers[i] = web.PlexServer{Name: cfg.Plex[i].Name, Plex: client}
	}
	return web.New(web.Options{
		Username: cfg.UI.Username,
		Password: cfg.UI.Password,
		Status:   status,
		Servers:  servers,
		Store:    st,
		Rules: store.DueRules{
			MinimumAge:         cfg.MinimumAge,
			RefreshInterval:    cfg.RefreshInterval,
			MaxRefreshInterval: cfg.MaxRefreshInterval,
			Ignored:            cfg.Ignored,
		},
		Secrets: uiSecrets(cfg, clients),
	})
}

// uiSecrets returns the values that the API redacts from stored error
// texts: the token, the URL host, and the path prefix of each Plex server,
// the TMDB token, and the UI password.
func uiSecrets(cfg config.Config, clients []*plex.Client) []string {
	secrets := make([]string, 0, 3*len(clients)+2)
	for i, client := range clients {
		s := cfg.Plex[i]
		secrets = append(secrets, s.Token, s.BaseURL.Host, client.PathPrefix())
	}
	return append(secrets, cfg.TMDBToken, cfg.UI.Password)
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
func stopUI(ui *web.Server) {
	if ui == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), uiStopTimeout)
	defer cancel()
	if err := ui.Stop(ctx); err != nil {
		log.Warn().Err(err).Msg("Stopping UI failed")
	}
}

// serveUntilStopped runs the schedule until ctx ends. If ctx already ended,
// for example during the token checks, the schedule never starts. Then it
// stops in this order: the UI, so that no request still reads the store,
// then the schedule and the busy run. The schedule has its own context, so
// a busy run goes on while the UI stops. ui can be nil.
func serveUntilStopped(ctx context.Context, ui *web.Server, r *runner, sched cron.Schedule) {
	schedCtx, cancelSched := context.WithCancel(context.Background())
	defer cancelSched()
	var served sync.WaitGroup
	if ctx.Err() == nil {
		served.Go(func() { serve(schedCtx, r, sched) })
	}
	<-ctx.Done()
	stopUI(ui)
	cancelSched()
	served.Wait()
}
