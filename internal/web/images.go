package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

const (
	// photoTimeout bounds the Plex request of an image.
	photoTimeout = 30 * time.Second
	// defaultMaxImageFetches bounds concurrent upstream Plex photo fetches.
	defaultMaxImageFetches = 16
)

var errInvalidImage = errors.New("unsupported image content type")

type imageKey struct {
	srv, width, height int
	ratingKey, path    string
}

type imageCall struct {
	done        chan struct{}
	body        []byte
	contentType string
	err         error
}

// imageSize is the size of a scaled image, in pixels.
type imageSize struct{ width, height int }

// thumbSizes and artSizes are the sizes that the size parameter names.
var (
	thumbSizes = map[string]imageSize{"sm": {120, 180}, "md": {240, 360}, "lg": {480, 720}}
	artSizes   = map[string]imageSize{"sm": {480, 270}, "md": {960, 540}, "lg": {1920, 1080}}
)

// serveThumb answers GET /api/items/thumb with the poster of an item.
func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request) {
	s.serveImage(w, r, thumbSizes, func(it store.ListItem) string { return it.Thumb })
}

// serveArt answers GET /api/items/art with the background art of an item.
func (s *Server) serveArt(w http.ResponseWriter, r *http.Request) {
	s.serveImage(w, r, artSizes, func(it store.ListItem) string { return it.Art })
}

// serveImage proxies one image of a known item through the photo
// transcoder of its Plex server. Only an image that Plex gave for the item
// is requested, and only an image type is passed on, so the browser gets no
// Plex header and no foreign content.
func (s *Server) serveImage(w http.ResponseWriter, r *http.Request, sizes map[string]imageSize, field func(store.ListItem) string) {
	v, err := queryParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	size, ok := sizeParam(v, sizes)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid size")
		return
	}
	srv, it, ok := s.knownItem(w, r, v)
	if !ok {
		return
	}
	imagePath := plex.ItemImagePath(it.RatingKey, field(it))
	if imagePath == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	key := imageKey{srv: srv, ratingKey: it.RatingKey, path: imagePath, width: size.width, height: size.height}
	body, contentType, err := s.fetchImage(r.Context(), key)
	if err != nil {
		s.writeImageError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(body)
}

func (s *Server) writeImageError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		log.Debug().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("Image proxy request failed")
		return
	}
	if errors.Is(err, plex.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	log.Warn().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("Image proxy request failed")
	writeError(w, http.StatusBadGateway, "image unavailable")
}

// fetchImage returns the validated image bytes and content type for key.
// Identical concurrent requests share one upstream Plex call, and distinct
// upstream fetches are bounded by s.imageSem.
func (s *Server) fetchImage(ctx context.Context, key imageKey) ([]byte, string, error) {
	if call := s.lookupImageCall(key); call != nil {
		return waitImageCall(ctx, call)
	}
	select {
	case s.imageSem <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	call, leader := s.startImageCall(key)
	if !leader {
		<-s.imageSem
		return waitImageCall(ctx, call)
	}
	go s.runImageCall(key, call)
	return waitImageCall(ctx, call)
}

func (s *Server) lookupImageCall(key imageKey) *imageCall {
	s.imageMu.Lock()
	defer s.imageMu.Unlock()
	return s.imageCalls[key]
}

func (s *Server) startImageCall(key imageKey) (*imageCall, bool) {
	s.imageMu.Lock()
	defer s.imageMu.Unlock()
	if call := s.imageCalls[key]; call != nil {
		return call, false
	}
	call := &imageCall{done: make(chan struct{})}
	s.imageCalls[key] = call
	return call, true
}

func (s *Server) runImageCall(key imageKey, call *imageCall) {
	defer func() {
		<-s.imageSem
		s.imageMu.Lock()
		delete(s.imageCalls, key)
		s.imageMu.Unlock()
		close(call.done)
	}()
	ctx, cancel := context.WithTimeout(s.baseCtx, photoTimeout)
	defer cancel()
	body, err := s.servers[key.srv].Plex.Photo(ctx, key.ratingKey, key.path, key.width, key.height)
	if err != nil {
		call.err = err
		return
	}
	ct := sniffImage(body)
	if ct == "" {
		call.err = errInvalidImage
		return
	}
	call.body, call.contentType = body, ct
}

func waitImageCall(ctx context.Context, call *imageCall) ([]byte, string, error) {
	select {
	case <-call.done:
		return call.body, call.contentType, call.err
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}

// sizeParam returns the size that the size parameter names in sizes. It
// defaults to md.
func sizeParam(v url.Values, sizes map[string]imageSize) (imageSize, bool) {
	name := "md"
	if v.Has("size") {
		name = v.Get("size")
	}
	size, ok := sizes[name]
	return size, ok
}

// sniffImage returns the content type of body when it is a JPEG, PNG, or
// WebP image, and "" otherwise.
func sniffImage(body []byte) string {
	switch t := http.DetectContentType(body); t {
	case "image/jpeg", "image/png", "image/webp":
		return t
	}
	return ""
}
