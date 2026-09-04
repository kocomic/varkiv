#!/bin/sh
set -eu

exec /usr/bin/qemu-x86_64-static \
  -L /opt/amd64 \
  /opt/amd64/usr/lib/wine/wineserver64 -p0 "$@"
