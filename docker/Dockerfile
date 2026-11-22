# Certificates and timezone data are the same on every architecture, so this
# stage runs on the build platform and needs no emulation.
FROM --platform=$BUILDPLATFORM alpine:3.24 AS rootfs
RUN apk add --no-cache ca-certificates tzdata

RUN mkdir /config

FROM scratch

ARG TARGETOS
ARG TARGETARCH

ENV \
  PATH="/app" \
  AUTOREFRESH_CONFIG="/config/config.yml" \
  AUTOREFRESH_DATABASE="/config/autorefresh.db" \
  AUTOREFRESH_LOG="/config/activity.log" \
  AUTOREFRESH_VERBOSITY="0"

COPY --from=rootfs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=rootfs /usr/share/zoneinfo /usr/share/zoneinfo
# /config belongs to the default user, so a new volume at /config is
# writable without root. COPY sets root as the owner without --chown.
COPY --from=rootfs --chown=65532:65532 /config /config

# goreleaser v2 adds a suffix to each build folder: amd64_v1, arm64_v8.0
# and arm_7. The pattern matches exactly one folder for each platform.
COPY ["dist/autorefresh_${TARGETOS}_${TARGETARCH}_*/autorefresh", "/app/autorefresh"]

VOLUME ["/config"]

# The port of the dashboard. Nothing listens on it until ui.listen is set
# in config.yml, for example to ":7380".
EXPOSE 7380

# The default user is not root. Set your own UID:GID with --user, or with
# user: in compose. That user must own the directory at /config.
USER 65532:65532

ENTRYPOINT ["/app/autorefresh"]
