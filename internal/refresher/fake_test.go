package refresher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/l3uddz/autorefresh/internal/plex"
)

var _ Plex = (*plex.Client)(nil)

// fakePlex records each call and returns the configured results. A List,
// ItemPaths, or SearchMatches call without a configured result returns an
// empty result. Like plex.Client, it sends nothing once ctx is done: it
// records no call and returns an unavailable error that wraps ctx.Err().
type fakePlex struct {
	libraries      []plex.Library
	librariesErr   error
	lists          map[string][]plex.Item // by listKey
	listErrs       map[string]error       // by listKey
	episodeNumbers map[string]map[int][]int
	episodeReadErr error
	paths          map[string][]string         // by rating key
	pathErrs       map[string]error            // by rating key
	candidates     map[string][]plex.Candidate // by "<rating key> <search title>"
	searchErrs     map[string]error            // by "<rating key> <search title>", else by rating key
	searchYears    map[string]int              // the year of the last search, by "<rating key> <search title>"
	refreshErrs    map[string]error            // by rating key
	matchErrs      map[string]error            // by rating key
	series         map[string]plex.Series      // the provider series by GUID; a missing GUID is ErrNotFound
	seriesErrs     map[string]error            // by GUID
	matched        map[string]string           // the GUID of the last accepted match, by rating key
	calls          []string
	onCall         func(call string) // runs after each recorded call, if set
}

func newFakePlex(libraries ...plex.Library) *fakePlex {
	return &fakePlex{
		libraries:   libraries,
		lists:       map[string][]plex.Item{},
		listErrs:    map[string]error{},
		paths:       map[string][]string{},
		pathErrs:    map[string]error{},
		candidates:  map[string][]plex.Candidate{},
		searchErrs:  map[string]error{},
		refreshErrs: map[string]error{},
		matchErrs:   map[string]error{},
	}
}

// listKey renders a listing as "<section>?type=<type>&<filter>&...", with
// each filter written as Key=Value, for example "season.index>>=1".
func listKey(section string, itemType int, filters ...plex.Filter) string {
	parts := []string{"type=" + strconv.Itoa(itemType)}
	for _, f := range filters {
		parts = append(parts, f.Key+"="+f.Value)
	}
	return section + "?" + strings.Join(parts, "&")
}

func (f *fakePlex) record(call string) {
	f.calls = append(f.calls, call)
	if f.onCall != nil {
		f.onCall(call)
	}
}

// canceled returns the error of a call after ctx is done, or nil.
func canceled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("request failed: %w: %w", plex.ErrUnavailable, err)
	}
	return nil
}

func (f *fakePlex) Libraries(ctx context.Context) ([]plex.Library, error) {
	if err := canceled(ctx); err != nil {
		return nil, err
	}
	f.record("Libraries")
	return f.libraries, f.librariesErr
}

func (f *fakePlex) List(ctx context.Context, section string, itemType int, filters ...plex.Filter) ([]plex.Item, error) {
	if err := canceled(ctx); err != nil {
		return nil, err
	}
	key := listKey(section, itemType, filters...)
	f.record("List " + key)
	if err := f.listErrs[key]; err != nil {
		return nil, err
	}
	return f.lists[key], nil
}

// Item answers a check as if Plex had not loaded a new match yet: the item
// is still unmatched. checkedMetadata gives other answers.
func (f *fakePlex) Item(ctx context.Context, ratingKey string) (plex.Item, error) {
	if err := canceled(ctx); err != nil {
		return plex.Item{}, err
	}
	f.record("Item " + ratingKey)
	return unmatchedItem(ratingKey), nil
}

func (f *fakePlex) ItemPaths(ctx context.Context, ratingKey string) (plex.Paths, error) {
	if err := canceled(ctx); err != nil {
		return plex.Paths{}, err
	}
	f.record("ItemPaths " + ratingKey)
	if err := f.pathErrs[ratingKey]; err != nil {
		return plex.Paths{}, err
	}
	return plex.Paths{Locations: f.paths[ratingKey]}, nil
}

func (f *fakePlex) ItemWithPaths(ctx context.Context, ratingKey string) (plex.Item, plex.Paths, error) {
	if err := canceled(ctx); err != nil {
		return plex.Item{}, plex.Paths{}, err
	}
	f.record("ItemWithPaths " + ratingKey)
	if err := f.pathErrs[ratingKey]; err != nil {
		return plex.Item{}, plex.Paths{}, err
	}
	return unmatchedItem(ratingKey), plex.Paths{Locations: f.paths[ratingKey]}, nil
}

func (f *fakePlex) EpisodeNumbers(ctx context.Context, ratingKey string) (map[int][]int, error) {
	if err := canceled(ctx); err != nil {
		return nil, err
	}
	f.record("EpisodeNumbers " + ratingKey)
	return f.episodeNumbers[ratingKey], f.episodeReadErr
}

func (f *fakePlex) Refresh(ctx context.Context, ratingKey string) (plex.Outcome, error) {
	if err := canceled(ctx); err != nil {
		return plex.OutcomeUnknown, err
	}
	f.record("Refresh " + ratingKey)
	return fakeOutcome(f.refreshErrs[ratingKey])
}

// fakeOutcome returns the outcome that a Plex answer with err would give:
// accepted without an error, rejected for a 404, and unknown otherwise.
func fakeOutcome(err error) (plex.Outcome, error) {
	switch {
	case err == nil:
		return plex.OutcomeAccepted, nil
	case errors.Is(err, plex.ErrNotFound):
		return plex.OutcomeRejected, err
	default:
		return plex.OutcomeUnknown, err
	}
}

// SearchMatches records "SearchMatches <rating key> <title> <agent> <language>".
func (f *fakePlex) SearchMatches(ctx context.Context, ratingKey, title, agent, language string, year int) ([]plex.Candidate, error) {
	if err := canceled(ctx); err != nil {
		return nil, err
	}
	f.record(strings.Join([]string{"SearchMatches", ratingKey, title, agent, language}, " "))
	if f.searchYears == nil {
		f.searchYears = map[string]int{}
	}
	f.searchYears[ratingKey+" "+title] = year
	if err := f.searchErrs[ratingKey+" "+title]; err != nil {
		return nil, err
	}
	if err := f.searchErrs[ratingKey]; err != nil {
		return nil, err
	}
	return f.candidates[ratingKey+" "+title], nil
}

// ProviderSeries records "ProviderSeries <guid>".
func (f *fakePlex) ProviderSeries(ctx context.Context, guid string) (plex.Series, error) {
	if err := canceled(ctx); err != nil {
		return plex.Series{}, err
	}
	f.record("ProviderSeries " + guid)
	if err := f.seriesErrs[guid]; err != nil {
		return plex.Series{}, err
	}
	s, ok := f.series[guid]
	if !ok {
		return plex.Series{}, plex.ErrNotFound
	}
	return s, nil
}

// Match records "Match <rating key> <candidate guid>".
func (f *fakePlex) Match(ctx context.Context, ratingKey string, cand plex.Candidate) (plex.Outcome, error) {
	if err := canceled(ctx); err != nil {
		return plex.OutcomeUnknown, err
	}
	f.record("Match " + ratingKey + " " + cand.GUID)
	outcome, err := fakeOutcome(f.matchErrs[ratingKey])
	if err == nil {
		if f.matched == nil {
			f.matched = map[string]string{}
		}
		f.matched[ratingKey] = cand.GUID
	}
	return outcome, err
}
