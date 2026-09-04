#!/bin/sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
demo_root=${VARKIV_DEMO_DIR:-"$project_root/.demo"}
demo_db="$demo_root/library.db"
demo_addr=${VARKIV_DEMO_ADDR:-"127.0.0.1:8080"}

mkdir -p "$project_root/bin" "$demo_root"
cd "$project_root"
go build -o "$project_root/bin/varkiv" ./cmd/varkiv

"$project_root/bin/varkiv" import-pegasus \
  --db "$demo_db" \
  --library "$project_root/testdata/pegasus" \
  --source "$project_root/testdata/pegasus/gba/metadata.pegasus.txt" \
  --platform gba \
  --locale en

printf '\nDemo data: %s\nOpen: http://%s\n' "$demo_root" "$demo_addr"
exec "$project_root/bin/varkiv" serve \
  --db "$demo_db" \
  --state "$demo_root" \
  --library "$project_root/testdata/pegasus" \
  --addr "$demo_addr"
