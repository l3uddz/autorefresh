package plex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/l3uddz/autorefresh/internal/neterr"
)

var digitsPattern = regexp.MustCompile(`^[0-9]+$`)

// ItemImagePath returns p when it is the thumb or art path of the item, in
// the form that Plex gives. Otherwise it returns "".
func ItemImagePath(ratingKey, p string) string {
	if !idPattern.MatchString(ratingKey) {
		return ""
	}
	base := "/library/metadata/" + ratingKey + "/"
	for _, kind := range []string{"thumb/", "art/"} {
		if rest, ok := strings.CutPrefix(p, base+kind); ok && digitsPattern.MatchString(rest) {
			return p
		}
	}
	return ""
}

// SafeRemoteImage returns raw when it is an https URL with a host, without
// user info, fragment, or a token in its query. Otherwise it returns "".
func SafeRemoteImage(raw string) string {
	if strings.Contains(raw, "#") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	for key := range query {
		if strings.Contains(strings.ToLower(key), "token") {
			return ""
		}
	}
	return raw
}

// MaxPhotoBytes is the largest image that Photo accepts.
const MaxPhotoBytes = 10 << 20

// Photo returns the image at imagePath, scaled by the photo transcoder.
// imagePath must be the thumb or art path of ratingKey (ItemImagePath);
// otherwise it sends nothing.
func (c *Client) Photo(ctx context.Context, ratingKey, imagePath string, width, height int) ([]byte, error) {
	if ItemImagePath(ratingKey, imagePath) == "" {
		return nil, errors.New("photo: not an image path of the item")
	}
	query := url.Values{
		"url":     {imagePath},
		"width":   {strconv.Itoa(width)},
		"height":  {strconv.Itoa(height)},
		"minSize": {"1"},
		"upscale": {"1"},
	}
	endpoint := c.endpoint("photo", ":", "transcode")
	res, err := c.do(ctx, http.MethodGet, endpoint, query, "image/*")
	if err != nil {
		return nil, fmt.Errorf("photo: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxPhotoBytes+1))
	if err != nil {
		res.Body.Close()
		return nil, bodyError(http.MethodGet, endpoint.Path, err)
	}
	if len(body) > MaxPhotoBytes {
		res.Body.Close() // no draining: the server can stall after the limit
		return nil, errors.New("photo: larger than 10 MB")
	}
	neterr.DrainAndClose(res)
	return body, nil
}
