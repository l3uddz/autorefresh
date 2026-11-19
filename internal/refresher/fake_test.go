package refresher

import (
	"context"
	"strconv"
	"strings"

	"github.com/l3uddz/autorefresh/internal/plex"
)

var _ Plex = (*plex.Client)(nil)

// fakePlex records each call and returns the configured results. A List or
// ItemPaths call without a configured result returns an empty result.
type fakePlex struct {
	libraries    []plex.Library
	librariesErr error
	lists        map[string][]plex.Item // by listKey
	listErrs     map[string]error       // by listKey
	paths        map[string][]string    // by rating key
	pathErrs     map[string]error       // by rating key
	calls        []string
}

func newFakePlex(libraries ...plex.Library) *fakePlex {
	return &fakePlex{
		libraries: libraries,
		lists:     map[string][]plex.Item{},
		listErrs:  map[string]error{},
		paths:     map[string][]string{},
		pathErrs:  map[string]error{},
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

func (f *fakePlex) Libraries(context.Context) ([]plex.Library, error) {
	f.calls = append(f.calls, "Libraries")
	return f.libraries, f.librariesErr
}

func (f *fakePlex) List(_ context.Context, section string, itemType int, filters ...plex.Filter) ([]plex.Item, error) {
	key := listKey(section, itemType, filters...)
	f.calls = append(f.calls, "List "+key)
	if err := f.listErrs[key]; err != nil {
		return nil, err
	}
	return f.lists[key], nil
}

func (f *fakePlex) ItemPaths(_ context.Context, ratingKey string) ([]string, error) {
	f.calls = append(f.calls, "ItemPaths "+ratingKey)
	if err := f.pathErrs[ratingKey]; err != nil {
		return nil, err
	}
	return f.paths[ratingKey], nil
}

func (f *fakePlex) Refresh(_ context.Context, ratingKey string) error {
	f.calls = append(f.calls, "Refresh "+ratingKey)
	return nil
}

func (f *fakePlex) SearchMatches(_ context.Context, ratingKey, _, _, _ string) ([]plex.Candidate, error) {
	f.calls = append(f.calls, "SearchMatches "+ratingKey)
	return nil, nil
}

func (f *fakePlex) Match(_ context.Context, ratingKey string, _ plex.Candidate) error {
	f.calls = append(f.calls, "Match "+ratingKey)
	return nil
}
