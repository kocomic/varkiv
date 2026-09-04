FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40 AS build

ARG ALPINE_MIRROR=https://dl-cdn.alpinelinux.org
ENV SOURCE_DATE_EPOCH=1763597828

RUN sed -i "s#https://dl-cdn.alpinelinux.org#${ALPINE_MIRROR}#" /etc/apk/repositories \
    && apk add --no-cache build-base ca-certificates git linux-headers zlib-dev

WORKDIR /src
RUN git clone --filter=blob:none --no-checkout https://github.com/libretro/RetroArch.git RetroArch \
    && git -C RetroArch fetch --depth 1 origin 69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576 \
    && git -C RetroArch checkout --detach FETCH_HEAD \
    && test "$(git -C RetroArch rev-parse HEAD)" = "69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576" \
    && git clone --filter=blob:none --no-checkout https://github.com/EmulatorJS/snes9x.git snes9x \
    && git -C snes9x fetch --depth 1 origin 6ca2343e5f3b0acbea49ca958251e3a0af58a81d \
    && git -C snes9x checkout --detach FETCH_HEAD \
    && test "$(git -C snes9x rev-parse HEAD)" = "6ca2343e5f3b0acbea49ca958251e3a0af58a81d"

RUN cd RetroArch \
    && ./configure \
    && make -j"$(nproc)"
RUN make -C snes9x/libretro -j"$(nproc)" \
    && strings snes9x/libretro/snes9x_libretro.so | grep -F "1.63 6ca2343"

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
ARG ALPINE_MIRROR=https://dl-cdn.alpinelinux.org
RUN sed -i "s#https://dl-cdn.alpinelinux.org#${ALPINE_MIRROR}#" /etc/apk/repositories \
    && apk add --no-cache libstdc++ zlib \
    && addgroup -S -g 10001 acceptance \
    && adduser -S -D -H -u 10001 -G acceptance acceptance \
    && mkdir -p /home/acceptance \
    && chown 10001:10001 /home/acceptance
COPY --from=build /src/RetroArch/retroarch /usr/local/bin/retroarch
COPY --from=build /src/snes9x/libretro/snes9x_libretro.so /opt/libretro/snes9x_libretro.so
LABEL org.opencontainers.image.title="Varkiv SNES save compatibility acceptance" \
      org.varkiv.retroarch.version="1.22.2" \
      org.varkiv.retroarch.commit="69a4f0ea1e8aaf442ae4858f2e7f2b31a1776576" \
      org.varkiv.snes9x.version="1.63" \
      org.varkiv.snes9x.commit="6ca2343e5f3b0acbea49ca958251e3a0af58a81d"
USER 10001:10001
WORKDIR /evidence
ENTRYPOINT ["/usr/local/bin/retroarch"]
