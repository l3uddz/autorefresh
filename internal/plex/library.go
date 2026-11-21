package plex

import (
	"context"
	"errors"
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

// pageSize is the number of items requested per metadata page.
const pageSize = 500

var errIncompleteListing = errors.New("incomplete plex listing")

// Library types.
const (
	MovieLibrary = "movie"
	ShowLibrary  = "show"
)

// Library is one Plex library section.
type Library struct {
	ID, Title, Type, Agent, Language string // Type: MovieLibrary or ShowLibrary
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

// Filter is one Plex filter, sent as the query parameter Key=Value. The key
// holds the field and the operator without its "=", for example
// {Key: "addedAt>>", Value: "1795025872"} sends addedAt>>=1795025872, and
// {Key: "episode.summary", Value: ""} sends episode.summary=.
type Filter struct{ Key, Value string }

type metadata struct {
	RatingKey string `json:"ratingKey"`
	Index     int    `json:"index"`
	Season    *int   `json:"parentIndex"`
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
		Size      *int       `json:"size"`
		TotalSize *int       `json:"totalSize"` // nil when absent
		Metadata  []metadata `json:"Metadata"`
	} `json:"MediaContainer"`
}

func (m metadata) files() []string {
	var files []string
	for _, media := range m.Media {
		for _, part := range media.Part {
			if strings.TrimSpace(part.File) != "" {
				files = append(files, part.File)
			}
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

	if err := c.send(ctx, http.MethodGet, c.endpoint("library", "sections"), nil, &resp); err != nil {
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

// List reads every page of one item type in a section. It fails if the
// listing changes before the offset of a page during the read, because the
// page could then skip an item. Items that Plex adds at the end of the
// listing do not make it fail.
func (c *Client) List(ctx context.Context, section string, itemType int, filters ...Filter) ([]Item, error) {
	if !idPattern.MatchString(section) {
		return nil, errors.New("invalid plex section")
	}
	endpoint := c.endpoint("library", "sections", section, "all")
	var items []Item
	_, err := c.walkMetadataPages(ctx, endpoint, listQuery(itemType, filters), func(m metadata) {
		items = append(items, m.item())
	})
	if err != nil {
		return nil, fmt.Errorf("listing section %s: %w", section, err)
	}
	return items, nil
}

// walkMetadataPages visits one page at a time and returns the final total.
// Each page after the first overlaps the previous page by one item.
func (c *Client) walkMetadataPages(ctx context.Context, endpoint *url.URL, query url.Values, visit func(metadata)) (int, error) {
	query.Set("X-Plex-Container-Size", strconv.Itoa(pageSize))
	var read int
	var last string
	for {
		start := max(0, read-1)
		query.Set("X-Plex-Container-Start", strconv.Itoa(start))
		var resp metadataResponse
		if err := c.send(ctx, http.MethodGet, endpoint, query, &resp); err != nil {
			return 0, err
		}

		rest, err := unreadMetadata(resp, read, last)
		if err != nil {
			return 0, err
		}
		for _, item := range rest {
			visit(item)
			last = item.RatingKey
		}
		read += len(rest)
		total := *resp.MediaContainer.TotalSize
		switch {
		case read >= total:
			return total, nil
		case len(rest) == 0:
			return 0, fmt.Errorf("%w: page added no new items at offset %d of %d", errIncompleteListing, read, total)
		}
	}
}

// unreadMetadata excludes the overlap anchor. Without totalSize the end of
// the listing is unknown, since a short page can come before the end.
func unreadMetadata(resp metadataResponse, read int, last string) ([]metadata, error) {
	page, total, size := resp.MediaContainer.Metadata, resp.MediaContainer.TotalSize, resp.MediaContainer.Size
	if total == nil || *total < 0 {
		return nil, fmt.Errorf("%w: invalid or missing totalSize", errIncompleteListing)
	}
	if size != nil && *size != len(page) {
		return nil, fmt.Errorf("%w: reported size %d for %d entries", errIncompleteListing, *size, len(page))
	}
	if read == 0 {
		return page, nil
	}
	if last == "" || len(page) == 0 || page[0].RatingKey != last {
		return nil, fmt.Errorf("%w: listing changed during the read at offset %d", errIncompleteListing, read-1)
	}
	return page[1:], nil
}

func listQuery(itemType int, filters []Filter) url.Values {
	// The listing needs only item fields, so it leaves out the heavy elements.
	exclude := "Role,Director,Writer,Genre,Country"
	if itemType == TypeShow {
		exclude = "Media," + exclude
	}
	query := url.Values{
		"type":            {strconv.Itoa(itemType)},
		"sort":            {"addedAt"},
		"excludeElements": {exclude},
	}
	for _, f := range filters {
		query.Add(f.Key, f.Value)
	}
	return query
}

// ItemPaths returns Location[].path of a series, or Media[].Part[].file of a
// movie. Only a 404 status gives ErrNotFound. A response without an item
// gives a plain error.
func (c *Client) ItemPaths(ctx context.Context, ratingKey string) ([]string, error) {
	endpoint, err := c.metadataEndpoint(ratingKey)
	if err != nil {
		return nil, err
	}

	var resp metadataResponse
	if err := c.send(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, fmt.Errorf("item paths: %w", err)
	}
	if len(resp.MediaContainer.Metadata) == 0 {
		return nil, errors.New("item paths: response has no item")
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

// EpisodeNumbers returns the file-backed episode numbers in each regular
// season of a series. An incomplete listing gives no evidence.
func (c *Client) EpisodeNumbers(ctx context.Context, ratingKey string) (map[int][]int, error) {
	endpoint, err := c.metadataEndpoint(ratingKey, "allLeaves")
	if err != nil {
		return nil, err
	}
	seasons := make(map[int][]int)
	var read int
	valid := true
	total, err := c.walkMetadataPages(ctx, endpoint, url.Values{}, func(m metadata) {
		read++
		switch {
		case m.Season != nil && *m.Season == 0:
		case m.Season == nil || *m.Season < 0 || m.Index <= 0 || len(m.files()) == 0:
			valid = false
		default:
			seasons[*m.Season] = append(seasons[*m.Season], m.Index)
		}
	})
	if errors.Is(err, errIncompleteListing) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("episode numbers: %w", err)
	}
	if !valid || read != total {
		return nil, nil
	}
	return seasons, nil
}
