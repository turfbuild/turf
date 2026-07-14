#!/usr/bin/env bash
# Generate an "assembly" (BOM-of-BOMs) CycloneDX SBOM for the Turf installable
# unit — the turf CLI plus the turf-mcp-server binary, distributed together in
# one archive.
#
# It deliberately does NOT re-inventory dependencies. Instead it references the
# two component SBOMs (SBOM #1 = the turf CLI, SBOM #2 = the turf-mcp-server
# binary, produced/rewritten in their own repos) by:
#   * a CycloneDX BOM-Link  (urn:cdx:<serialNumber>/<version>) — a stable,
#     tooling-resolvable identifier for the exact sub-BOM, and
#   * a resolvable release-asset URL — where the sub-BOM is actually published,
# each carrying the sub-BOM's sha256 so a consumer can fetch and integrity-check
# it. This is the CycloneDX-idiomatic way to express a composite/assembly BOM.
#
# Usage:
#   assembly-sbom.sh <cli-sbom.cdx.json> <server-sbom.cdx.json> \
#                    <version> <release-base-url> <out.cdx.json>
# where <release-base-url> is e.g.
#   https://github.com/turfbuild/turf/releases/download/<tag>
set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <cli-sbom.cdx.json> <server-sbom.cdx.json> <version> <release-base-url> <out.cdx.json>" >&2
  exit 2
fi

CLI_SBOM=$1
SRV_SBOM=$2
VERSION=$3
BASE_URL=${4%/} # strip a trailing slash so "$BASE_URL/name" is clean
OUT=$5

for f in "$CLI_SBOM" "$SRV_SBOM"; do
  [[ -r "$f" ]] || { echo "cannot read SBOM: $f" >&2; exit 1; }
done

sha256() { shasum -a 256 "$1" | awk '{print $1}'; }

# CycloneDX serialNumber is "urn:uuid:<uuid>"; a BOM-Link uses the bare uuid:
#   urn:cdx:<uuid>/<bom-version>
bomlink() {
  local serial=$1 ver=$2
  printf 'urn:cdx:%s/%s' "${serial#urn:uuid:}" "$ver"
}

cli_serial=$(jq -r '.serialNumber // empty' "$CLI_SBOM")
cli_ver=$(jq -r '.version // 1' "$CLI_SBOM")
srv_serial=$(jq -r '.serialNumber // empty' "$SRV_SBOM")
srv_ver=$(jq -r '.version // 1' "$SRV_SBOM")

[[ -n "$cli_serial" ]] || { echo "CLI SBOM ($CLI_SBOM) has no serialNumber — need one for a BOM-Link" >&2; exit 1; }
[[ -n "$srv_serial" ]] || { echo "server SBOM ($SRV_SBOM) has no serialNumber — need one for a BOM-Link" >&2; exit 1; }

cli_sha=$(sha256 "$CLI_SBOM")
srv_sha=$(sha256 "$SRV_SBOM")
cli_url="$BASE_URL/$(basename "$CLI_SBOM")"
srv_url="$BASE_URL/$(basename "$SRV_SBOM")"

uuid=$(uuidgen 2>/dev/null | tr '[:upper:]' '[:lower:]' || cat /proc/sys/kernel/random/uuid)
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)

jq -n \
  --arg uuid "$uuid" \
  --arg ts "$ts" \
  --arg version "$VERSION" \
  --arg cli_link "$(bomlink "$cli_serial" "$cli_ver")" \
  --arg srv_link "$(bomlink "$srv_serial" "$srv_ver")" \
  --arg cli_url "$cli_url" \
  --arg srv_url "$srv_url" \
  --arg cli_sha "$cli_sha" \
  --arg srv_sha "$srv_sha" \
  '{
    bomFormat: "CycloneDX",
    specVersion: "1.6",
    serialNumber: ("urn:uuid:" + $uuid),
    version: 1,
    metadata: {
      timestamp: $ts,
      tools: { components: [ {
        type: "application",
        group: "github.com/turfbuild/turf",
        name: "assembly-sbom.sh"
      } ] },
      component: {
        "bom-ref": ("turf-bundle@" + $version),
        type: "application",
        name: "turf",
        version: $version,
        description: "Turf installable unit — the turf CLI plus the turf-mcp-server binary, distributed together in one archive."
      }
    },
    externalReferences: [
      { type: "bom", url: $cli_link, comment: "turf CLI component SBOM (CycloneDX BOM-Link)",
        hashes: [ { alg: "SHA-256", content: $cli_sha } ] },
      { type: "bom", url: $cli_url,  comment: "turf CLI component SBOM (release asset)",
        hashes: [ { alg: "SHA-256", content: $cli_sha } ] },
      { type: "bom", url: $srv_link, comment: "turf-mcp-server component SBOM (CycloneDX BOM-Link)",
        hashes: [ { alg: "SHA-256", content: $srv_sha } ] },
      { type: "bom", url: $srv_url,  comment: "turf-mcp-server component SBOM (release asset)",
        hashes: [ { alg: "SHA-256", content: $srv_sha } ] }
    ]
  }' > "$OUT"

jq -e . "$OUT" > /dev/null
echo "wrote assembly SBOM -> $OUT"
echo "  turf CLI       : $(bomlink "$cli_serial" "$cli_ver")  sha256=$cli_sha"
echo "  turf-mcp-server: $(bomlink "$srv_serial" "$srv_ver")  sha256=$srv_sha"
