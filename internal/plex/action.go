package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Candidate is one match that Plex offers for an item.
type Candidate struct {
	GUID, Name string
	Year       int // 0 when absent
}

// Refresh asks Plex to refresh the metadata of one item.
func (c *Client) Refresh(ctx context.Context, ratingKey string) error {
	if err := checkRatingKey(ratingKey); err != nil {
		return err
	}

	if err := c.send(ctx, http.MethodPut, "/library/metadata/"+ratingKey+"/refresh", nil, nil); err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	return nil
}

// SearchMatches asks Plex for the matches of one item.
func (c *Client) SearchMatches(ctx context.Context, ratingKey, title, agent, language string) ([]Candidate, error) {
	if err := checkRatingKey(ratingKey); err != nil {
		return nil, err
	}

	query := url.Values{
		"manual":   {"1"},
		"title":    {title},
		"agent":    {agent},
		"language": {language},
	}

	var resp struct {
		MediaContainer struct {
			SearchResult []struct {
				GUID string `json:"guid"`
				Name string `json:"name"`
				Year int    `json:"year"`
			} `json:"SearchResult"`
		} `json:"MediaContainer"`
	}
	if err := c.send(ctx, http.MethodGet, "/library/metadata/"+ratingKey+"/matches", query, &resp); err != nil {
		return nil, fmt.Errorf("search matches: %w", err)
	}

	candidates := make([]Candidate, 0, len(resp.MediaContainer.SearchResult))
	for _, r := range resp.MediaContainer.SearchResult {
		candidates = append(candidates, Candidate{GUID: r.GUID, Name: r.Name, Year: r.Year})
	}
	return candidates, nil
}

// Match applies a candidate to one item.
func (c *Client) Match(ctx context.Context, ratingKey string, cand Candidate) error {
	if err := checkRatingKey(ratingKey); err != nil {
		return err
	}

	query := url.Values{"guid": {cand.GUID}, "name": {cand.Name}}
	if cand.Year != 0 {
		query.Set("year", strconv.Itoa(cand.Year))
	}

	if err := c.send(ctx, http.MethodPut, "/library/metadata/"+ratingKey+"/match", query, nil); err != nil {
		return fmt.Errorf("match: %w", err)
	}
	return nil
}
