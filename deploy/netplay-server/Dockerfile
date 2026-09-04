# syntax=docker/dockerfile:1.7

FROM rust:1.90-alpine3.22@sha256:b4b54b176a74db7e5c68fdfe6029be39a02ccbcfe72b6e5a3e18e2c61b57ae26 AS build
ENV CARGO_REGISTRIES_CRATES_IO_PROTOCOL=sparse
RUN apk add --no-cache musl-dev
COPY cargo-config.toml /usr/local/cargo/config.toml
WORKDIR /build
ADD --checksum=sha256:b83e809e3ff7de5d58836ca63c2020e54ee16fa604e642d04568a7805e57a71d \
    https://github.com/EmulatorJS/EmulatorJS-netplay/archive/4090ca7bda795a8b7a7596f4d41a4605b515d9c5.tar.gz /tmp/source.tar.gz
RUN tar -xzf /tmp/source.tar.gz --strip-components=1 -C /build \
    && cargo build --locked --release

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
LABEL org.opencontainers.image.title="Varkiv EmulatorJS netplay sidecar" \
      org.opencontainers.image.source="https://github.com/EmulatorJS/EmulatorJS-netplay" \
      org.opencontainers.image.revision="4090ca7bda795a8b7a7596f4d41a4605b515d9c5" \
      org.opencontainers.image.licenses="Apache-2.0"
RUN addgroup -S -g 10002 netplay \
    && adduser -S -D -H -u 10002 -G netplay netplay
COPY --from=build /build/target/release/rust-socket-server /usr/local/bin/emulatorjs-netplay
COPY --from=build /build/LICENSE /usr/share/licenses/emulatorjs-netplay/LICENSE

USER 10002:10002
ENV PORT=3000
EXPOSE 3000
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=4 \
  CMD wget -q -O /dev/null http://127.0.0.1:3000/games || exit 1
ENTRYPOINT ["/usr/local/bin/emulatorjs-netplay"]
