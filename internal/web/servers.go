package web

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/store"
)

// identityTimeout bounds each Plex identity request.
const identityTimeout = 3 * time.Second

type serversJSON struct {
	Servers       []serverJSON `json:"servers"`
	IgnoreReasons []string     `json:"ignoreReasons"`
}

type serverJSON struct {
	Srv               int    `json:"srv"`
	Name              string `json:"name"`
	MachineIdentifier string `json:"machineIdentifier,omitempty"`
	// Scanned is false until the first scan of the server ended.
	Scanned   bool          `json:"scanned"`
	Libraries []libraryJSON `json:"libraries"`
}

type libraryJSON struct {
	Section      string    `json:"section"`
	Title        string    `json:"title"`
	Type         string    `json:"type"`
	LastRead     time.Time `json:"lastRead,omitzero"`
	LastFullRead time.Time `json:"lastFullRead,omitzero"`
}

// serveServers answers GET /api/servers with the configured servers, their
// libraries, and the ignored reasons.
func (s *Server) serveServers(w http.ResponseWriter, r *http.Request) {
	servers := make([]serverJSON, len(s.servers))
	for i, ps := range s.servers {
		info, err := s.serverInfo(r.Context(), i, ps.Name)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		servers[i] = info
	}
	for i, id := range s.machineIdentifiers(r.Context()) {
		servers[i].MachineIdentifier = id
	}
	writeJSON(w, http.StatusOK, serversJSON{Servers: servers, IgnoreReasons: reasonNames(s.rules.Ignored)})
}

// serverInfo reads the libraries of the server srv and whether its first
// scan ended.
func (s *Server) serverInfo(ctx context.Context, srv int, name string) (serverJSON, error) {
	libs, err := s.store.Libraries(ctx, name)
	if err != nil {
		return serverJSON{}, err
	}
	info := serverJSON{Srv: srv, Name: name, Libraries: make([]libraryJSON, 0, len(libs))}
	for _, l := range libs {
		info.Libraries = append(info.Libraries, libraryJSON{
			Section: l.Section, Title: l.Title, Type: l.Type, LastRead: l.LastRead, LastFullRead: l.LastFullRead,
		})
		if !l.LastRead.IsZero() {
			info.Scanned = true
		}
	}
	if len(libs) > 0 {
		return info, nil
	}
	// A scan that found no library leaves no library row, only its run.
	runs, err := s.store.Runs(ctx, store.RunQuery{Servers: []string{name}, Status: store.RunFinished, Limit: 1})
	if err != nil {
		return serverJSON{}, err
	}
	info.Scanned = len(runs) > 0
	return info, nil
}

// machineIdentifiers returns the machine identifier of each server, "" for
// one that Plex did not tell. It asks Plex in parallel for each identifier
// that is not cached yet.
func (s *Server) machineIdentifiers(ctx context.Context) []string {
	s.idMu.Lock()
	ids := slices.Clone(s.identities)
	s.idMu.Unlock()

	var wg sync.WaitGroup
	for i, id := range ids {
		if id == "" {
			wg.Go(func() { ids[i] = s.readIdentity(ctx, i) })
		}
	}
	wg.Wait()
	return ids
}

// readIdentity asks the server srv for its machine identifier and caches it.
// A failure gives "", so that the next request asks again.
func (s *Server) readIdentity(ctx context.Context, srv int) string {
	ctx, cancel := context.WithTimeout(ctx, identityTimeout)
	defer cancel()
	ps := s.servers[srv]
	id, err := ps.Plex.Identity(ctx)
	if err != nil {
		log.Debug().Str("server", ps.Name).Err(err).Msg("Reading Plex identity failed")
		return ""
	}
	s.idMu.Lock()
	s.identities[srv] = id
	s.idMu.Unlock()
	return id
}
