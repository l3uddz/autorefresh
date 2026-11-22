package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// photoTimeout bounds the Plex request of an image.
const photoTimeout = 30 * time.Second

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
	size, ok := sizeParam(r.URL.Query(), sizes)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid size")
		return
	}
	srv, it, ok := s.knownItem(w, r)
	if !ok {
		return
	}
	imagePath := plex.ItemImagePath(it.RatingKey, field(it))
	if imagePath == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), photoTimeout)
	body, err := s.servers[srv].Plex.Photo(ctx, it.RatingKey, imagePath, size.width, size.height)
	cancel()
	if errors.Is(err, plex.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	contentType := sniffImage(body)
	if err != nil || contentType == "" {
		writeError(w, http.StatusBadGateway, "image unavailable")
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(body)
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
