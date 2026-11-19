package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog/log"
)

// Release variables. goreleaser and the mise build task set them.
var (
	Version   = "0.0.0-dev"
	GitCommit = "unknown"
	Timestamp = "unknown"
)

var cli struct {
	Version kong.VersionFlag `help:"Print version information and quit"`

	// An empty path means a file in the default config directory. main
	// resolves it after parsing, so a set path never creates that directory.
	Log       string `type:"path" env:"AUTOREFRESH_LOG" help:"Log file path (default: activity.log in the default config directory)"`
	Verbosity int    `type:"counter" default:"0" short:"v" env:"AUTOREFRESH_VERBOSITY" help:"Log level verbosity"`
}

func main() {
	version := fmt.Sprintf("%s (%s@%s)", Version, GitCommit, Timestamp)

	// parse cli. kong.Parse exits if the arguments are not valid.
	kong.Parse(&cli,
		kong.Name("autorefresh"),
		kong.Description("Refresh Plex items that have missing metadata"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Summary: true,
			Compact: true,
		}),
		kong.Vars{
			"version": version,
		},
	)

	if cli.Log == "" {
		cli.Log = filepath.Join(defaultConfigDirectory("autorefresh", "config.yml"), "activity.log")
	}
	log.Logger = newLogger(os.Stderr, cli.Log, cli.Verbosity)

	log.Info().
		Str("version", version).
		Msg("Initialised")
}
