#!/bin/sh
set -eu

usage() {
  echo "usage: $0 [--all|--go|--android|--node]" >&2
  exit 2
}

mode=all
if [ "$#" -gt 1 ]; then
  usage
fi
if [ "$#" -eq 1 ]; then
  case "$1" in
    --all) mode=all ;;
    --go) mode=go ;;
    --android) mode=android ;;
    --node) mode=node ;;
    *) usage ;;
  esac
fi

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
lock_file="$repo_root/docs/third-party-inventory.lock.tsv"
notice_file="$repo_root/docs/THIRD_PARTY_NOTICES.md"
apache_license="$repo_root/docs/licenses/Apache-2.0.txt"
project_license="$repo_root/LICENSE"

for required in "$lock_file" "$notice_file" "$apache_license" "$project_license"; do
  if [ ! -s "$required" ]; then
    echo "missing third-party audit input: $required" >&2
    exit 1
  fi
done

if ! cmp -s "$project_license" "$apache_license"; then
  echo "project LICENSE must be the unmodified reviewed Apache-2.0 text" >&2
  exit 1
fi

audit_tmp=$(mktemp -d "${TMPDIR:-/tmp}/varkiv-license-audit.XXXXXX")
cleanup() {
  rm -rf -- "$audit_tmp"
}
trap cleanup EXIT HUP INT TERM

tab=$(printf '\t')
manifest_rows="$audit_tmp/manifest.tsv"
awk -F '\t' 'NF && $1 !~ /^#/ {
  if (NF != 4 || $1 == "" || $2 == "" || $3 == "" || $4 == "") {
    print "invalid third-party lock row at line " NR > "/dev/stderr"
    exit 1
  }
  print $1 "\t" $2 "\t" $3 "\t" $4
}' "$lock_file" | sort > "$manifest_rows"

if [ -n "$(cut -f1-3 "$manifest_rows" | uniq -d)" ]; then
  echo "duplicate runtime dependency in $lock_file" >&2
  exit 1
fi

while IFS="$tab" read -r scope coordinate version license; do
  line=$(grep -F "$coordinate" "$notice_file" || true)
  if [ -z "$line" ]; then
    echo "third-party notice is missing $coordinate" >&2
    exit 1
  fi
  if ! printf '%s\n' "$line" | grep -F "$version" >/dev/null; then
    echo "third-party notice has stale version for $coordinate (expected $version)" >&2
    exit 1
  fi
  if ! printf '%s\n' "$line" | grep -F "$license" >/dev/null; then
    echo "third-party notice has stale license for $coordinate (expected $license)" >&2
    exit 1
  fi
done < "$manifest_rows"

check_go() {
  expected="$audit_tmp/go-expected.tsv"
  actual="$audit_tmp/go-actual.tsv"
  actual_unsorted="$audit_tmp/go-actual-unsorted.tsv"
  awk -F '\t' '$1 == "go-runtime" { print $2 "\t" $3 }' "$manifest_rows" | sort > "$expected"
  : > "$actual_unsorted"
  while read -r goos goarch goarm; do
    if [ -n "$goarm" ]; then
      (
        cd "$repo_root"
        GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" CGO_ENABLED=0 \
          go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}' ./cmd/varkiv
      )
    else
      (
        cd "$repo_root"
        GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
          go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}' ./cmd/varkiv
      )
    fi
  done <<'TARGETS' | sed '/^$/d' | awk '{ print $1 "\t" $2 }' > "$actual_unsorted"
linux amd64
linux arm64
linux arm 7
windows amd64
windows arm64
darwin arm64
TARGETS
  sort -u "$actual_unsorted" > "$actual"
  if ! diff -u "$expected" "$actual"; then
    echo "Go release-matrix runtime dependency inventory drifted; update the lock and notices after license review" >&2
    exit 1
  fi
}

check_android() {
  expected="$audit_tmp/android-expected.tsv"
  actual="$audit_tmp/android-actual.tsv"
  report="$audit_tmp/android-dependencies.txt"
  awk -F '\t' '$1 == "android-runtime" { print $2 "\t" $3 }' "$manifest_rows" | sort > "$expected"
  (
    cd "$repo_root/clients/android"
    ./gradlew --no-daemon --console=plain -q :app:dependencies --configuration releaseRuntimeClasspath
  ) > "$report"
  awk '/--- / {
    sub(/^.*--- /, "")
    if ($2 == "->") {
      split($1, requested, ":")
      print requested[1] ":" requested[2] "\t" $3
    } else {
      split($1, selected, ":")
      if (selected[3] != "") print selected[1] ":" selected[2] "\t" selected[3]
    }
  }' "$report" | sort -u > "$actual"
  if ! diff -u "$expected" "$actual"; then
    echo "Android release runtime dependency inventory drifted; update the lock and notices after license review" >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    license_hash=$(sha256sum "$apache_license" | awk '{print $1}')
  else
    license_hash=$(shasum -a 256 "$apache_license" | awk '{print $1}')
  fi
  if [ "$license_hash" != "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30" ]; then
    echo "bundled Apache-2.0 text does not match the reviewed upstream copy" >&2
    exit 1
  fi
}

check_node() {
  package_lock="$repo_root/package-lock.json"
  expected="$audit_tmp/node-expected.tsv"
  actual="$audit_tmp/node-actual.tsv"
  if [ ! -s "$package_lock" ]; then
    echo "missing Node dependency lock: $package_lock" >&2
    exit 1
  fi
  awk -F '\t' '$1 == "node-development" { print $2 "\t" $3 "\t" $4 }' "$manifest_rows" | sort > "$expected"
  node - "$package_lock" <<'NODE' | sort -u > "$actual"
const fs = require('node:fs');
const lock = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
for (const [path, item] of Object.entries(lock.packages ?? {})) {
  if (!path || !path.includes('node_modules/')) continue;
  const coordinate = path.split('node_modules/').at(-1);
  if (!coordinate || !item.version || !item.license) {
    throw new Error(`incomplete package-lock identity for ${path || '<root>'}`);
  }
  process.stdout.write(`${coordinate}\t${item.version}\t${item.license}\n`);
}
NODE
  if ! diff -u "$expected" "$actual"; then
    echo "Node development dependency inventory drifted; update the lock and notices after license review" >&2
    exit 1
  fi
}

case "$mode" in
  all) check_go; check_android; check_node ;;
  go) check_go ;;
  android) check_android ;;
  node) check_node ;;
esac

echo "third_party_dependency_audit=passed mode=$mode"
