# autorefresh

Autorefresh finds Plex items with missing metadata, such as a missing summary
or poster, and asks Plex to refresh them. It runs on a schedule beside
[autoscan](https://github.com/l3uddz/autoscan).

The project is in early development. The current binary only prints its
version.

## Docker

CI publishes the image `l3uddz/autorefresh` to Docker Hub for
`linux/amd64`, `linux/arm64`, and `linux/arm/v7`.

| Tag | Content |
| --- | --- |
| `latest` | The latest release |
| `<version>` | A release, for example `1.0.0` |
| `main` | The latest commit on the `main` branch |

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
