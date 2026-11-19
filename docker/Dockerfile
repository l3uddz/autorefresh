# Certificates and timezone data are the same on every architecture, so this
# stage runs on the build platform and needs no emulation.
FROM --platform=$BUILDPLATFORM alpine:3.24 AS rootfs
RUN apk add --no-cache ca-certificates tzdata

FROM scratch

ARG TARGETOS
ARG TARGETARCH

ENV PATH="/app"

COPY --from=rootfs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=rootfs /usr/share/zoneinfo /usr/share/zoneinfo

# goreleaser v2 adds a suffix to each build folder: amd64_v1, arm64_v8.0
# and arm_7. The pattern matches exactly one folder for each platform.
COPY ["dist/autorefresh_${TARGETOS}_${TARGETARCH}_*/autorefresh", "/app/autorefresh"]

VOLUME ["/config"]

ENTRYPOINT ["/app/autorefresh"]
