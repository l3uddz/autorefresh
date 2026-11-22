package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureLog sets the global logger to trace level with a buffer. The test
// restores the previous logger and level when it ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldLogger, oldLevel := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = oldLogger
		zerolog.SetGlobalLevel(oldLevel)
	})
	return &buf
}

func TestCanceledRequestIsNoStoreFailure(t *testing.T) {
	s := newAPIServer(openStore(t), 0, "Home")
	for _, path := range []string{
		"/api/items",
		"/api/servers",
		"/api/runs",
		"/api/item?srv=0&key=101",
	} {
		t.Run(path, func(t *testing.T) {
			logs := captureLog(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			rec := serve(s, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
			if rec.Code == http.StatusInternalServerError || rec.Body.Len() != 0 {
				t.Errorf("answer = %d %s, want none", rec.Code, rec.Body)
			}
			if strings.Contains(logs.String(), `"level":"error"`) {
				t.Errorf("error log for a canceled request: %s", logs)
			}
			if !strings.Contains(logs.String(), `"level":"debug"`) {
				t.Errorf("no debug log for a canceled request: %s", logs)
			}
		})
	}
}
