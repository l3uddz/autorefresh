package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Item types in a section.
const (
	TypeMovie = 1
	TypeShow  = 2
)

// pageSize is the number of items that List asks for in each request.
const pageSize = 500

// Library is one Plex library section.
type Library struct {
	ID, Title, Type, Agent, Language string // Type: "movie" or "show"
}

// Item is one movie or series in a section listing.
type Item struct {
	RatingKey string
	GUID      string // "local://<ratingKey>" when unmatched
	Title     string
	Year      int // 0 when absent
	Summary   string
	Thumb     string // "" when absent
	Art       string // "" when absent
	AddedAt   time.Time
	Files     []string // movie listings: Media[].Part[].file
}

// Filter is one Plex filter, sent as Field+Op+Value, for example
// {Field: "addedAt", Op: ">>=", Value: "1795025872"} or
// {Field: "episode.summary", Op: "=", Value: ""}.
type Filter struct{ Field, Op, Value string }

type metadata struct {
	RatingKey string `json:"ratingKey"`
	GUID      string `json:"guid"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Summary   string `json:"summary"`
	Thumb     string `json:"thumb"`
	Art       string `json:"art"`
	AddedAt   int64  `json:"addedAt"`
	Media     []struct {
		Part []struct {
			File string `json:"file"`
		} `json:"Part"`
	} `json:"Media"`
	Location []struct {
		Path string `json:"path"`
	} `json:"Location"`
}

type metadataResponse struct {
	MediaContainer struct {
		TotalSize int        `json:"totalSize"`
		Metadata  []metadata `json:"Metadata"`
	} `json:"MediaContainer"`
}

func (m metadata) files() []string {
	var files []string
	for _, media := range m.Media {
		for _, part := range media.Part {
			files = append(files, part.File)
		}
	}
	return files
}

func (m metadata) item() Item {
	return Item{
		RatingKey: m.RatingKey,
		GUID:      m.GUID,
		Title:     m.Title,
		Year:      m.Year,
		Summary:   m.Summary,
		Thumb:     m.Thumb,
		Art:       m.Art,
		AddedAt:   time.Unix(m.AddedAt, 0),
		Files:     m.files(),
	}
}

// Libraries lists the library sections of the server.
func (c *Client) Libraries(ctx context.Context) ([]Library, error) {
	var resp struct {
		MediaContainer struct {
			Directory []struct {
				Key      string `json:"key"`
				Title    string `json:"title"`
				Type     string `json:"type"`
				Agent    string `json:"agent"`
				Language string `json:"language"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}

	if err := c.send(ctx, http.MethodGet, "/library/sections", nil, &resp); err != nil {
		return nil, fmt.Errorf("libraries: %w", err)
	}

	libraries := make([]Library, 0, len(resp.MediaContainer.Directory))
	for _, d := range resp.MediaContainer.Directory {
		libraries = append(libraries, Library{
			ID: d.Key, Title: d.Title, Type: d.Type, Agent: d.Agent, Language: d.Language,
		})
	}
	return libraries, nil
}

// List reads every page of one item type in a section.
func (c *Client) List(ctx context.Context, section string, itemType int, filters ...Filter) ([]Item, error) {
	endpoint := "/library/sections/" + url.PathEscape(section) + "/all"

	// The listing needs only item fields, so it leaves out the heavy elements.
	exclude := "Role,Director,Writer,Genre,Country"
	if itemType == TypeShow {
		exclude = "Media," + exclude
	}

	var items []Item
	for {
		query := url.Values{
			"type":                   {strconv.Itoa(itemType)},
			"sort":                   {"addedAt"},
			"excludeElements":        {exclude},
			"X-Plex-Container-Start": {strconv.Itoa(len(items))},
			"X-Plex-Container-Size":  {strconv.Itoa(pageSize)},
		}
		for _, f := range filters {
			query.Add(f.Field+strings.TrimSuffix(f.Op, "="), f.Value)
		}

		var resp metadataResponse
		if err := c.send(ctx, http.MethodGet, endpoint, query, &resp); err != nil {
			return nil, fmt.Errorf("listing section %s: %w", section, err)
		}

		page := resp.MediaContainer.Metadata
		if len(page) == 0 && len(items) < resp.MediaContainer.TotalSize {
			return nil, fmt.Errorf("listing section %s: empty page at offset %d of %d",
				section, len(items), resp.MediaContainer.TotalSize)
		}
		for _, m := range page {
			items = append(items, m.item())
		}
		if len(items) >= resp.MediaContainer.TotalSize {
			return items, nil
		}
	}
}

// ItemPaths returns Location[].path of a series, or Media[].Part[].file of a
// movie.
func (c *Client) ItemPaths(ctx context.Context, ratingKey string) ([]string, error) {
	if err := checkRatingKey(ratingKey); err != nil {
		return nil, err
	}

	var resp metadataResponse
	if err := c.send(ctx, http.MethodGet, "/library/metadata/"+ratingKey, nil, &resp); err != nil {
		return nil, fmt.Errorf("item paths: %w", err)
	}
	if len(resp.MediaContainer.Metadata) == 0 {
		return nil, fmt.Errorf("item paths: %w", ErrNotFound)
	}

	m := resp.MediaContainer.Metadata[0]
	if len(m.Location) == 0 {
		return m.files(), nil
	}
	paths := make([]string, 0, len(m.Location))
	for _, l := range m.Location {
		paths = append(paths, l.Path)
	}
	return paths, nil
}
