package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Candidate is one match that Plex offers for an item.
type Candidate struct {
	GUID, Name string
	Year       int // 0 when absent
	Type       string
	Summary    string
	Thumb      string // an https URL without credentials, or "" when unsafe
	Score      int
}

// Refresh asks Plex to refresh the metadata of one item.
func (c *Client) Refresh(ctx context.Context, ratingKey string) error {
	endpoint, err := c.metadataEndpoint(ratingKey, "refresh")
	if err != nil {
		return err
	}

	if err := c.send(ctx, http.MethodPut, endpoint, nil, nil); err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	return nil
}

// SearchMatches asks Plex for the matches of one item.
func (c *Client) SearchMatches(ctx context.Context, ratingKey, title, agent, language string, year int) ([]Candidate, error) {
	endpoint, err := c.metadataEndpoint(ratingKey, "matches")
	if err != nil {
		return nil, err
	}

	query := url.Values{
		"manual":   {"1"},
		"title":    {title},
		"agent":    {agent},
		"language": {language},
	}

	if year > 0 {
		query.Set("year", strconv.Itoa(year))
	}

	var resp struct {
		MediaContainer struct {
			SearchResult []json.RawMessage `json:"SearchResult"`
		} `json:"MediaContainer"`
	}
	if err := c.send(ctx, http.MethodGet, endpoint, query, &resp); err != nil {
		return nil, fmt.Errorf("search matches: %w", err)
	}

	candidates := make([]Candidate, 0, len(resp.MediaContainer.SearchResult))
	for _, raw := range resp.MediaContainer.SearchResult {
		cand, err := decodeCandidate(raw)
		if err != nil {
			return nil, fmt.Errorf("search matches: %w", err)
		}
		candidates = append(candidates, cand)
	}
	return candidates, nil
}

// decodeCandidate reads one search result. The fields that a match uses
// must decode. The other fields are only shown, so a value of an unexpected
// type leaves only that field empty: json.Unmarshal skips it and still fills
// the rest.
func decodeCandidate(raw json.RawMessage) (Candidate, error) {
	var match struct {
		GUID string `json:"guid"`
		Name string `json:"name"`
		Year int    `json:"year"`
	}
	if err := json.Unmarshal(raw, &match); err != nil {
		return Candidate{}, fmt.Errorf("decoding candidate: %w", err)
	}
	var shown struct {
		Type    string `json:"type"`
		Summary string `json:"summary"`
		Thumb   string `json:"thumb"`
		Score   int    `json:"score"`
	}
	var typeErr *json.UnmarshalTypeError
	if err := json.Unmarshal(raw, &shown); err != nil && !errors.As(err, &typeErr) {
		return Candidate{}, fmt.Errorf("decoding candidate: %w", err)
	}
	return Candidate{
		GUID: match.GUID, Name: match.Name, Year: match.Year, Type: shown.Type,
		Summary: shown.Summary, Thumb: SafeRemoteImage(shown.Thumb), Score: shown.Score,
	}, nil
}

// Match applies a candidate to one item.
func (c *Client) Match(ctx context.Context, ratingKey string, cand Candidate) error {
	endpoint, err := c.metadataEndpoint(ratingKey, "match")
	if err != nil {
		return err
	}

	query := url.Values{"guid": {cand.GUID}, "name": {cand.Name}}
	if cand.Year != 0 {
		query.Set("year", strconv.Itoa(cand.Year))
	}

	if err := c.send(ctx, http.MethodPut, endpoint, query, nil); err != nil {
		return fmt.Errorf("match: %w", err)
	}
	return nil
}
