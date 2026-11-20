package refresher

import (
	"context"
	"strconv"
	"strings"

	"github.com/l3uddz/autorefresh/internal/plex"
)

var _ Plex = (*plex.Client)(nil)

// fakePlex records each call and returns the configured results. A List,
// ItemPaths, or SearchMatches call without a configured result returns an
// empty result.
type fakePlex struct {
	libraries    []plex.Library
	librariesErr error
	lists        map[string][]plex.Item      // by listKey
	listErrs     map[string]error            // by listKey
	paths        map[string][]string         // by rating key
	pathErrs     map[string]error            // by rating key
	candidates   map[string][]plex.Candidate // by "<rating key> <search title>"
	searchErrs   map[string]error            // by "<rating key> <search title>", else by rating key
	refreshErrs  map[string]error            // by rating key
	matchErrs    map[string]error            // by rating key
	calls        []string
	onCall       func(call string) // runs after each recorded call, if set
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
// each filter written as Field+Op+Value, for example "season.index>>=1".
func listKey(section string, itemType int, filters ...plex.Filter) string {
	parts := []string{"type=" + strconv.Itoa(itemType)}
	for _, f := range filters {
		parts = append(parts, f.Field+f.Op+f.Value)
	}
	return section + "?" + strings.Join(parts, "&")
}

func (f *fakePlex) record(call string) {
	f.calls = append(f.calls, call)
	if f.onCall != nil {
		f.onCall(call)
	}
}

func (f *fakePlex) Libraries(context.Context) ([]plex.Library, error) {
	f.record("Libraries")
	return f.libraries, f.librariesErr
}

func (f *fakePlex) List(_ context.Context, section string, itemType int, filters ...plex.Filter) ([]plex.Item, error) {
	key := listKey(section, itemType, filters...)
	f.record("List " + key)
	if err := f.listErrs[key]; err != nil {
		return nil, err
	}
	return f.lists[key], nil
}

func (f *fakePlex) ItemPaths(_ context.Context, ratingKey string) ([]string, error) {
	f.record("ItemPaths " + ratingKey)
	if err := f.pathErrs[ratingKey]; err != nil {
		return nil, err
	}
	return f.paths[ratingKey], nil
}

func (f *fakePlex) Refresh(_ context.Context, ratingKey string) error {
	f.record("Refresh " + ratingKey)
	return f.refreshErrs[ratingKey]
}

// SearchMatches records "SearchMatches <rating key> <title> <agent> <language>".
func (f *fakePlex) SearchMatches(_ context.Context, ratingKey, title, agent, language string) ([]plex.Candidate, error) {
	f.record(strings.Join([]string{"SearchMatches", ratingKey, title, agent, language}, " "))
	if err := f.searchErrs[ratingKey+" "+title]; err != nil {
		return nil, err
	}
	if err := f.searchErrs[ratingKey]; err != nil {
		return nil, err
	}
	return f.candidates[ratingKey+" "+title], nil
}

// Match records "Match <rating key> <candidate guid>".
func (f *fakePlex) Match(_ context.Context, ratingKey string, cand plex.Candidate) error {
	f.record("Match " + ratingKey + " " + cand.GUID)
	return f.matchErrs[ratingKey]
}
