#!/bin/sh
set -eu

export WINELOADERNOEXEC=1

exec /usr/bin/qemu-x86_64-static \
  -0 /opt/amd64/usr/lib/wine/wine64 \
  -L /opt/amd64 \
  /opt/amd64/usr/lib/wine/wine64-amd64 "$@"
