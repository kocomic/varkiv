# syntax=docker/dockerfile:1.7

FROM golang:1.26.6-alpine3.23@sha256:e57c41c1d5864341031181b0db34b9a537bb5773eb6428e4e5bdaea0f9135406 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY scripts/check-third-party-notices.sh scripts/collect-third-party-licenses.sh ./scripts/
COPY LICENSE ./LICENSE
COPY docs/THIRD_PARTY_NOTICES.md docs/third-party-inventory.lock.tsv ./docs/
COPY docs/licenses/Apache-2.0.txt ./docs/licenses/Apache-2.0.txt
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/varkiv ./cmd/varkiv
RUN ./scripts/collect-third-party-licenses.sh /out/third-party-licenses --go-only

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
LABEL org.opencontainers.image.title="Varkiv" \
      org.opencontainers.image.description="Self-hosted personal ROM library and device integration service" \
      org.opencontainers.image.licenses="Apache-2.0"
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 varkiv \
    && adduser -S -D -H -u 10001 -G varkiv varkiv \
    && mkdir -p /data /library \
    && chown -R varkiv:varkiv /data /library

COPY --from=build /out/varkiv /usr/local/bin/varkiv
COPY --from=build /out/third-party-licenses /usr/share/doc/varkiv/THIRD_PARTY_LICENSES
COPY LICENSE /usr/share/licenses/varkiv/LICENSE

USER 10001:10001
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/api/v1/health/ready || exit 1

ENTRYPOINT ["/usr/local/bin/varkiv"]
CMD ["serve", "--db", "/data/library.db", "--state", "/data", "--library", "/library", "--addr", "0.0.0.0:8080"]
