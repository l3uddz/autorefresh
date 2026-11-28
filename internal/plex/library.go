package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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
	// ExternalIDs are the external IDs of a matched item, such as
	// "tmdb://100". Movie listings and item reads ask for them.
	ExternalIDs []string
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
	// ExternalGuids receives the "Guid" list of external IDs. An exact key
	// match wins over the case-insensitive one, so the list cannot fall into GUID.
	ExternalGuids json.RawMessage `json:"Guid"`
	Title         string          `json:"title"`
	Year          int             `json:"year"`
	Summary       string          `json:"summary"`
	Thumb         string          `json:"thumb"`
	Art           string          `json:"art"`
	AddedAt       int64           `json:"addedAt"`
	ChildCount    int             `json:"childCount"`
	LeafCount     int             `json:"leafCount"`
	Media         []struct {
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
		RatingKey:   m.RatingKey,
		GUID:        m.GUID,
		Title:       m.Title,
		Year:        m.Year,
		Summary:     m.Summary,
		Thumb:       m.Thumb,
		Art:         m.Art,
		AddedAt:     time.Unix(m.AddedAt, 0),
		Files:       m.files(),
		ExternalIDs: m.externalIDs(),
	}
}

// externalIDs returns the id of each entry of the Guid list. A Guid value
// that is not a list of objects gives no IDs, so only the comparison that
// uses them has nothing to compare, and the listing does not fail.
func (m metadata) externalIDs() []string {
	if len(m.ExternalGuids) == 0 {
		return nil
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(m.ExternalGuids, &entries); err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

// Identity returns the machine identifier of the server.
func (c *Client) Identity(ctx context.Context) (string, error) {
	var resp struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if err := c.send(ctx, http.MethodGet, c.endpoint(), nil, &resp); err != nil {
		return "", fmt.Errorf("identity: %w", err)
	}
	id := resp.MediaContainer.MachineIdentifier
	if !identityPattern.MatchString(id) {
		return "", errors.New("identity: invalid machine identifier")
	}
	return id, nil
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
	_, err := c.walkMetadataPages(ctx, endpoint, listQuery(itemType, filters), func(m metadata) bool {
		items = append(items, m.item())
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("listing section %s: %w", section, err)
	}
	return items, nil
}

// findPageSize is the most items that FindByGUID asks for.
const findPageSize = 50

// FindByGUID lists the items of one item type in a section that Plex
// gives for the filter guid=<guid>. It reads one page of at most
// findPageSize items and no further page, so a Plex version that ignores
// the filter costs one small page. The caller checks the GUID of each item.
func (c *Client) FindByGUID(ctx context.Context, section string, itemType int, guid string) ([]Item, error) {
	switch {
	case !idPattern.MatchString(section):
		return nil, errors.New("invalid plex section")
	case itemType != TypeMovie && itemType != TypeShow:
		return nil, fmt.Errorf("invalid plex item type %d", itemType)
	case guid == "":
		return nil, errors.New("empty plex guid")
	}
	query := listQuery(itemType, []Filter{{Key: "guid", Value: guid}})
	query.Set("X-Plex-Container-Start", "0")
	query.Set("X-Plex-Container-Size", strconv.Itoa(findPageSize))
	var resp metadataResponse
	if err := c.send(ctx, http.MethodGet, c.endpoint("library", "sections", section, "all"), query, &resp); err != nil {
		return nil, fmt.Errorf("finding guid in section %s: %w", section, err)
	}
	items := make([]Item, 0, len(resp.MediaContainer.Metadata))
	for _, m := range resp.MediaContainer.Metadata {
		items = append(items, m.item())
	}
	return items, nil
}

// walkMetadataPages visits one page at a time and returns the final total.
// Each page after the first overlaps the previous page by one item. Returning
// false from visit stops pagination early.
func (c *Client) walkMetadataPages(ctx context.Context, endpoint *url.URL, query url.Values, visit func(metadata) bool) (int, error) {
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
			if !visit(item) {
				return 0, nil
			}
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
	if itemType == TypeMovie {
		// The external IDs let a read find a wrong match with no extra request.
		query.Set("includeGuids", "1")
	}
	for _, f := range filters {
		query.Add(f.Key, f.Value)
	}
	return query
}

// Paths holds the paths that Plex gives for one item.
type Paths struct {
	Locations []string // Location[].path
	Files     []string // Media[].Part[].file
}

// All returns the Locations, or else the Files. It is the set of paths that
// match evaluation uses.
func (p Paths) All() []string {
	if len(p.Locations) > 0 {
		return p.Locations
	}
	return p.Files
}

// itemMetadata reads the first metadata entry of an item. Only a 404 status
// gives ErrNotFound. A response without an item gives a plain error. The
// errors have the prefix op.
func (c *Client) itemMetadata(ctx context.Context, ratingKey, op string) (metadata, error) {
	endpoint, err := c.metadataEndpoint(ratingKey)
	if err != nil {
		return metadata{}, err
	}

	var resp metadataResponse
	if err := c.send(ctx, http.MethodGet, endpoint, url.Values{"includeGuids": {"1"}}, &resp); err != nil {
		return metadata{}, fmt.Errorf("%s: %w", op, err)
	}
	if len(resp.MediaContainer.Metadata) == 0 {
		return metadata{}, fmt.Errorf("%s: response has no item", op)
	}
	return resp.MediaContainer.Metadata[0], nil
}

func (m metadata) paths() Paths {
	var locations []string
	for _, l := range m.Location {
		locations = append(locations, l.Path)
	}
	return Paths{Locations: locations, Files: m.files()}
}

// Item reads the metadata for one item. Only a 404 gives ErrNotFound.
func (c *Client) Item(ctx context.Context, ratingKey string) (Item, error) {
	m, err := c.itemMetadata(ctx, ratingKey, "item")
	if err != nil {
		return Item{}, err
	}
	return m.item(), nil
}

// ItemWithPaths reads the metadata and paths for one item in one request.
// Only a 404 gives ErrNotFound.
func (c *Client) ItemWithPaths(ctx context.Context, ratingKey string) (Item, Paths, error) {
	m, err := c.itemMetadata(ctx, ratingKey, "item")
	if err != nil {
		return Item{}, Paths{}, err
	}
	return m.item(), m.paths(), nil
}

// ItemPaths returns Location[].path and Media[].Part[].file of an item. Only
// a 404 status gives ErrNotFound. A response without an item gives a plain
// error.
func (c *Client) ItemPaths(ctx context.Context, ratingKey string) (Paths, error) {
	m, err := c.itemMetadata(ctx, ratingKey, "item paths")
	if err != nil {
		return Paths{}, err
	}
	return m.paths(), nil
}

// Details holds the fields of one item that the dashboard shows.
type Details struct {
	GUID, Summary         string
	ChildCount, LeafCount int // seasons and episodes of a show; 0 otherwise
	Paths                 Paths
	ExternalIDs           []string // such as "tmdb://100"
}

// ItemDetails reads the details of an item. Errors follow ItemPaths.
func (c *Client) ItemDetails(ctx context.Context, ratingKey string) (Details, error) {
	m, err := c.itemMetadata(ctx, ratingKey, "item details")
	if err != nil {
		return Details{}, err
	}
	return Details{
		GUID: m.GUID, Summary: m.Summary,
		ChildCount: m.ChildCount, LeafCount: m.LeafCount,
		Paths: m.paths(), ExternalIDs: m.externalIDs(),
	}, nil
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
	total, err := c.walkMetadataPages(ctx, endpoint, url.Values{}, func(m metadata) bool {
		read++
		switch {
		case m.Season != nil && *m.Season == 0:
		case m.Season == nil || *m.Season < 0 || m.Index <= 0 || len(m.files()) == 0:
			valid = false
			return false
		default:
			seasons[*m.Season] = append(seasons[*m.Season], m.Index)
		}
		return true
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
