package main

import (
	"fmt"

	"github.com/alecthomas/kong"
)

// Release variables. goreleaser and the mise build task set them.
var (
	Version   = "0.0.0-dev"
	GitCommit = "unknown"
	Timestamp = "unknown"
)

var cli struct {
	Version kong.VersionFlag `help:"Print version information and quit"`
}

func main() {
	kong.Parse(&cli,
		kong.Name("autorefresh"),
		kong.Description("Refresh Plex items that have missing metadata"),
		kong.UsageOnError(),
		kong.Vars{
			"version": fmt.Sprintf("%s (%s@%s)", Version, GitCommit, Timestamp),
		},
	)
}
