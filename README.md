# autorefresh

Autorefresh finds Plex items with missing metadata, such as a missing summary
or poster, and asks Plex to refresh them. It runs on a schedule beside
[autoscan](https://github.com/l3uddz/autoscan).

The project is in early development. The current binary only starts its
logger, writes one log line, and stops.

## Logging

Autorefresh writes each log line to stderr and to a log file. When the log
file reaches 5 MB, autorefresh starts a new file. It keeps 5 old files and
deletes files after 14 days.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--log` | `AUTOREFRESH_LOG` | `activity.log` in the default configuration directory | Log file path |
| `-v`, `--verbosity` | `AUTOREFRESH_VERBOSITY` | `0` | Log level: `0` is info, `1` is debug, `2` or more is trace |

If the directory of the binary contains `config.yml`, the default
configuration directory is that directory. Otherwise it is `autorefresh` in
the user configuration directory, for example `~/.config/autorefresh` on
Linux.

The Docker image sets `AUTOREFRESH_LOG=/config/activity.log`.

## Docker

CI publishes the image `l3uddz/autorefresh` to Docker Hub for
`linux/amd64`, `linux/arm64`, and `linux/arm/v7`.

| Tag | Content |
| --- | --- |
| `latest` | The latest release |
| `<version>` | A release, for example `1.0.0` |
| `main` | The latest commit on the `main` branch |
| `<branch>` | The latest build of an open PR from that branch. Each `/` in the branch name becomes `-` |

```sh
docker run --rm l3uddz/autorefresh:main --version
```

## Build from source

You need [mise](https://mise.jdx.dev). It installs the Go and goreleaser
versions from `mise.toml`.

```sh
mise install
mise run build
```

The binary is in `dist/autorefresh_<os>_<arch>/`. Run `mise tasks` to list
the other tasks.

## License

[MIT](LICENSE)
