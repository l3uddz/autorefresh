package web

import (
	"errors"
	"sync"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

// Health is the last known state of the connection to a server.
type Health string

// The health states.
const (
	HealthUnknown      Health = "unknown"
	HealthOK           Health = "ok"
	HealthUnreachable  Health = "unreachable"
	HealthUnauthorized Health = "unauthorized"
)

// tmdbOff is the TMDB state when no TMDB token is configured.
const tmdbOff = "off"

// StatusView is a snapshot of the live status.
type StatusView struct {
	Servers []ServerStatus `json:"servers"`
	TMDB    string         `json:"tmdb"` // "off", or a Health
}

// ServerStatus is the live status of one server.
type ServerStatus struct {
	Srv    int          `json:"srv"`
	Name   string       `json:"name"`
	Health Health       `json:"health"`
	Last   *RunProgress `json:"last,omitempty"`
	Run    *RunProgress `json:"run"` // nil while idle
}

// RunProgress is the progress of the run of one server. Refreshed, Matched,
// and Failed are the request counts of the run as of the last finished
// item, with the meaning of the same fields of the stored run.
type RunProgress struct {
	ID            int64           `json:"id"`
	Kind          store.RunKind   `json:"kind"`
	Source        store.RunSource `json:"source"`
	Status        store.RunStatus `json:"status"`
	FinishedAt    time.Time       `json:"finishedAt,omitzero"`
	Error         string          `json:"error,omitempty"`
	Recorded      bool            `json:"recorded"`
	StartedAt     time.Time       `json:"startedAt"`
	LibrariesRead int             `json:"librariesRead"`
	Library       string          `json:"library,omitempty"` // the last library read
	Selected      int             `json:"selected"`
	Due           int             `json:"due"`
	Done          int             `json:"done"`
	Refreshed     int             `json:"refreshed"`
	Matched       int             `json:"matched"`
	Failed        int             `json:"failed"`
	Skipped       int             `json:"skipped"`
	Current       string          `json:"current,omitempty"` // the title in work
}

// Status keeps the live status of each server and of TMDB, and signals each
// change to its subscribers. It is the refresher.Progress of the service.
type Status struct {
	mu      sync.Mutex
	servers []ServerStatus
	index   map[string]int
	tmdb    string
	subs    map[chan struct{}]struct{}
}

var _ refresher.Progress = (*Status)(nil)

// NewStatus returns the status of servers, in their configured order. With
// tmdb false, the TMDB state is "off".
func NewStatus(servers []string, tmdb bool) *Status {
	s := &Status{
		servers: make([]ServerStatus, len(servers)),
		index:   make(map[string]int, len(servers)),
		tmdb:    tmdbOff,
		subs:    make(map[chan struct{}]struct{}),
	}
	for i, name := range servers {
		s.servers[i] = ServerStatus{Srv: i, Name: name, Health: HealthUnknown}
		s.index[name] = i
	}
	if tmdb {
		s.tmdb = string(HealthUnknown)
	}
	return s
}

// Snapshot returns a deep copy of the status, so encoding it never reads
// shared state.
func (s *Status) Snapshot() StatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	servers := make([]ServerStatus, len(s.servers))
	for i, srv := range s.servers {
		if srv.Run != nil {
			run := *srv.Run
			srv.Run = &run
		}
		if srv.Last != nil {
			last := *srv.Last
			srv.Last = &last
		}
		servers[i] = srv
	}
	return StatusView{Servers: servers, TMDB: s.tmdb}
}

// ServerChecked records the result of the start check of server.
func (s *Status) ServerChecked(server string, err error) {
	s.updateServer(server, func(srv *ServerStatus) {
		srv.Health = checkHealth(err)
	})
}

// TMDBChecked records the result of the start check of TMDB. It does nothing
// when TMDB is off.
func (s *Status) TMDBChecked(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tmdb == tmdbOff {
		return
	}
	s.tmdb = string(tmdbHealth(err))
	s.notify()
}

// RunStarted starts the progress of a run of server.
func (s *Status) RunStarted(id int64, start store.RunStart) {
	s.updateServer(start.Server, func(srv *ServerStatus) {
		srv.Run = &RunProgress{ID: id, Kind: start.Kind, Source: start.Source, StartedAt: start.StartedAt, Status: store.RunRunning}
	})
}

// LibraryRead counts a library that the run of server read.
func (s *Status) LibraryRead(server, library string) {
	s.updateRun(server, func(run *RunProgress) {
		run.LibrariesRead++
		run.Library = library
	})
}

// Batch records the items that the run of server selected out of the due
// items.
func (s *Status) Batch(server string, selected, due int) {
	s.updateRun(server, func(run *RunProgress) {
		run.Selected = selected
		run.Due = due
	})
}

// ItemStarted records the item that the run of server works on.
func (s *Status) ItemStarted(server, _, title string) {
	s.updateRun(server, func(run *RunProgress) {
		run.Current = title
	})
}

// ItemDone counts a finished item of the run of server and takes the
// request counts of the run so far from the refresher.
func (s *Status) ItemDone(server, _, _ string, counts refresher.Counts) {
	s.updateRun(server, func(run *RunProgress) {
		run.Done++
		run.Current = ""
		run.Refreshed = counts.Refreshed
		run.Matched = counts.Matched
		run.Failed = counts.Failed
		run.Skipped = counts.Skipped
	})
}

// RunFinished ends the progress of the run of server and updates its health
// from the result.
func (s *Status) RunFinished(server string, res store.RunResult, err error, recorded bool) {
	s.updateServer(server, func(srv *ServerStatus) {
		if srv.Run != nil {
			last := *srv.Run
			last.Current = ""
			last.Refreshed, last.Matched, last.Failed, last.Skipped = res.Refreshed, res.Matched, res.Failed, res.Skipped
			last.Selected, last.Due = res.Selected, res.Due
			last.Status, last.FinishedAt, last.Error, last.Recorded = res.Status, res.FinishedAt, res.Error, recorded
			srv.Last = &last
		}
		srv.Run = nil
		srv.Health = runHealth(srv.Health, res, err)
	})
}

// updateServer applies change to the status of server and signals the
// subscribers. It ignores an unknown server.
func (s *Status) updateServer(server string, change func(*ServerStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.index[server]
	if !ok {
		return
	}
	change(&s.servers[i])
	s.notify()
}

// updateRun applies change to the run of server. It ignores a server that
// has no run.
func (s *Status) updateRun(server string, change func(*RunProgress)) {
	s.updateServer(server, func(srv *ServerStatus) {
		if srv.Run != nil {
			change(srv.Run)
		}
	})
}

// subscribe returns a channel that receives a signal after a change, and a
// function that ends the subscription. Signals do not queue: a subscriber
// that has not read the last one gets no new one.
func (s *Status) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// notify signals each subscriber without blocking. The caller holds s.mu.
func (s *Status) notify() {
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// checkHealth returns the health that a start check with err shows.
func checkHealth(err error) Health {
	switch {
	case err == nil:
		return HealthOK
	case errors.Is(err, plex.ErrUnauthorized):
		return HealthUnauthorized
	default:
		return HealthUnreachable
	}
}

// runHealth returns the health after a run with res and err. A run that
// shows nothing about the connection keeps current.
func runHealth(current Health, res store.RunResult, err error) Health {
	switch {
	case errors.Is(err, plex.ErrUnauthorized):
		return HealthUnauthorized
	case errors.Is(err, plex.ErrUnavailable):
		return HealthUnreachable
	case err == nil && res.Status == store.RunFinished:
		return HealthOK
	default:
		return current
	}
}

// tmdbHealth returns the health that a TMDB check with err shows.
func tmdbHealth(err error) Health {
	switch {
	case err == nil:
		return HealthOK
	case errors.Is(err, tmdb.ErrUnauthorized):
		return HealthUnauthorized
	default:
		return HealthUnreachable
	}
}
