#!/usr/bin/env bash
# Pack the npm distribution of turf-mcp-server: one launcher package and one
# package per platform, laid out as Restate's own @restatedev/restate-server.
#
# Usage: scripts/npm-pack.sh <version> <bin dir> <out dir> [legal dir]
#   e.g. scripts/npm-pack.sh v1.5.0 dist dist/npm
#
# <bin dir> holds the server-side binaries as a release attaches them:
# <name>-<os>_<arch> for name in turf-mcp-server, turf-engine,
# turf-builtin-provider, turf-ui.
#
# [legal dir] holds the LICENSE (and NOTICE, if any) every package carries; it
# defaults to this checkout's release/. npm-publish.yml passes the copies of the
# tag being packed, so a package carries the licence of the release it wraps.
#
# Writes to <out dir>:
#   turfbuild-turf-mcp-server-<v>.tgz                @turfbuild/turf-mcp-server
#   turfbuild-turf-mcp-server-<os>-<cpu>-<v>.tgz     @turfbuild/turf-mcp-server-<os>-<cpu>
# The launcher (npm/turf-mcp-server/bin/turf-mcp-server.js) runs the platform
# package's bin/turf-mcp-server, which finds the other three beside itself.
# restate-server is Restate's own package, a dependency at
# RESTATE_SERVER_VERSION below; it is never repackaged here.
#
# NPM_PLATFORMS selects the platforms to pack (Go names, space-separated;
# default all four). A selected platform missing any binary fails the run. The
# launcher always names all four as optional dependencies.
set -euo pipefail

# The one pin of restate-server for the npm distribution: the version Turf is
# tested with. Turf's local engine moves restate's data forward one minor
# version at a time, so move this pin one minor at a time.
RESTATE_SERVER_VERSION="1.8.1"

VERSION="${1:-}"; BIN_DIR="${2:-}"; OUT_DIR="${3:-}"; LEGAL_DIR="${4:-}"
if [[ -z "$VERSION" || -z "$BIN_DIR" || -z "$OUT_DIR" ]]; then
  echo "usage: $0 <version> <bin dir> <out dir> [legal dir]   (e.g. $0 v1.5.0 dist dist/npm)" >&2
  exit 1
fi
VERSION="${VERSION#v}"
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: '$VERSION' is not a semver version (x.y.z or x.y.z-pre)" >&2
  exit 1
fi
[[ -d "$BIN_DIR" ]] || { echo "error: no directory $BIN_DIR" >&2; exit 1; }
command -v npm >/dev/null || { echo "error: npm not found" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SRC="$ROOT/npm/turf-mcp-server"
LEGAL_DIR="${LEGAL_DIR:-$ROOT/release}"
[[ -f "$LEGAL_DIR/LICENSE" ]] || { echo "error: $LEGAL_DIR/LICENSE is missing" >&2; exit 1; }
LEGAL_DIR="$(cd "$LEGAL_DIR" && pwd)"
BIN_DIR="$(cd "$BIN_DIR" && pwd)"
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"
STAGE="$OUT_DIR/.stage"

BINARIES=(turf-mcp-server turf-engine turf-builtin-provider turf-ui)
read -r -a PLATFORMS <<< "${NPM_PLATFORMS:-darwin/arm64 darwin/amd64 linux/amd64 linux/arm64}"

# Check every selected platform before packing any.
missing=0
for platform in "${PLATFORMS[@]}"; do
  os="${platform%/*}"; arch="${platform#*/}"
  case "$platform" in darwin/arm64|darwin/amd64|linux/arm64|linux/amd64) ;; *)
    echo "error: unsupported platform $platform" >&2; exit 1 ;; esac
  for b in "${BINARIES[@]}"; do
    if [[ ! -f "$BIN_DIR/$b-${os}_${arch}" ]]; then
      echo "error: $BIN_DIR/$b-${os}_${arch} is missing" >&2
      missing=1
    fi
  done
done
[[ "$missing" == 0 ]] || exit 1

# Common files for a staged package: the legal texts and the README.
legal() {
  cp "$LEGAL_DIR/LICENSE" "$1/LICENSE"
  if [[ -f "$LEGAL_DIR/NOTICE" ]]; then cp "$LEGAL_DIR/NOTICE" "$1/NOTICE"; fi
  cp "$SRC/README.md" "$1/README.md"
}

render() {
  local tmpl="$1" out="$2" os="${3:-}" cpu="${4:-}"
  sed -e "s/@VERSION@/$VERSION/g" -e "s/@RESTATE_VERSION@/$RESTATE_SERVER_VERSION/g" \
      -e "s/@OS@/$os/g" -e "s/@CPU@/$cpu/g" "$tmpl" > "$out"
}

pack() {
  ( cd "$1" && npm pack --silent --pack-destination "$OUT_DIR" ) >/dev/null
}

rm -rf "$STAGE"
mkdir -p "$STAGE"

for platform in "${PLATFORMS[@]}"; do
  os="${platform%/*}"; arch="${platform#*/}"
  cpu="$arch"; [[ "$arch" == amd64 ]] && cpu=x64
  name="turf-mcp-server-$os-$cpu"
  dir="$STAGE/$name"
  mkdir -p "$dir/bin"
  for b in "${BINARIES[@]}"; do
    cp "$BIN_DIR/$b-${os}_${arch}" "$dir/bin/$b"
    chmod 0755 "$dir/bin/$b"
  done
  legal "$dir"
  render "$SRC/platform.package.json.tmpl" "$dir/package.json" "$os" "$cpu"
  rm -f "$OUT_DIR/turfbuild-$name-$VERSION.tgz"
  pack "$dir"
  echo "$OUT_DIR/turfbuild-$name-$VERSION.tgz"
done

dir="$STAGE/turf-mcp-server"
mkdir -p "$dir/bin"
cp "$SRC/bin/turf-mcp-server.js" "$dir/bin/turf-mcp-server.js"
chmod 0755 "$dir/bin/turf-mcp-server.js"
legal "$dir"
render "$SRC/package.json.tmpl" "$dir/package.json"
rm -f "$OUT_DIR/turfbuild-turf-mcp-server-$VERSION.tgz"
pack "$dir"
echo "$OUT_DIR/turfbuild-turf-mcp-server-$VERSION.tgz"

rm -rf "$STAGE"
