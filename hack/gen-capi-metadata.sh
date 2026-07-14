#!/usr/bin/env bash
set -euo pipefail

# Generates the clusterctl metadata.yaml (the map of release series to CAPI
# contract versions) from the repository's git tags. The file is not tracked
# in the repo: every vX.Y.Z tag contributes a releaseSeries entry for its X.Y
# series, so the complete file can be regenerated at any time and each release
# attaches a fresh copy alongside infrastructure-components.yaml.
#
# Every series is stamped with the same contract. If the provider ever
# migrates to a newer CAPI contract, add a boundary here mapping series older
# than the migration to the previous contract.

CONTRACT="v1beta2"
OUTPUT=""
VERSION=""

usage() {
  cat <<EOF
Generate clusterctl metadata.yaml from git tags.

Emits a releaseSeries entry for the major.minor series of every vX.Y.Z tag,
plus the optional <version> argument. The release target passes the tag being
cut, which also asserts the checkout is on an exact vX.Y.Z tag.

Usage:
  $(basename "$0") [flags] [<version>]

Args:
  <version>        additional version whose series must be included, e.g. v0.2.0

Flags:
  -c, --contract   CAPI contract version stamped on every entry (default: ${CONTRACT})
  -o, --output     file to write; parent directories are created (default: stdout)
  -h, --help       show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -c|--contract)
      [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 1; }
      CONTRACT="$2"
      shift 2
      ;;
    -o|--output)
      [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 1; }
      OUTPUT="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "error: unknown flag: $1" >&2
      usage >&2
      exit 1
      ;;
    *)
      [[ -z "$VERSION" ]] || { echo "error: unexpected argument: $1" >&2; usage >&2; exit 1; }
      VERSION="$1"
      shift
      ;;
  esac
done

if [[ -n "$VERSION" && ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
  echo "error: version \"${VERSION}\" is not in vX.Y.Z form" >&2
  exit 1
fi

cd "$(git rev-parse --show-toplevel)"

# All unique "major minor" pairs, sorted numerically.
series="$(
  { git tag -l 'v*'; if [[ -n "$VERSION" ]]; then echo "$VERSION"; fi; } \
    | sed -nE 's/^v([0-9]+)\.([0-9]+)\.[0-9]+.*$/\1 \2/p' \
    | sort -u -k1,1n -k2,2n
)"

if [[ -z "$series" ]]; then
  echo "error: no vX.Y.Z tags found and no <version> given; nothing to generate" >&2
  exit 1
fi

metadata="$(
  cat <<EOF
apiVersion: clusterctl.cluster.x-k8s.io/v1alpha3
kind: Metadata
releaseSeries:
EOF
  while read -r major minor; do
    cat <<EOF
  - major: ${major}
    minor: ${minor}
    contract: ${CONTRACT}
EOF
  done <<<"$series"
)"

if [[ -n "$OUTPUT" ]]; then
  mkdir -p "$(dirname "$OUTPUT")"
  printf '%s\n' "$metadata" >"$OUTPUT"
  echo "generated ${OUTPUT} ($(wc -l <<<"$series" | tr -d ' ') release series, contract ${CONTRACT})" >&2
else
  printf '%s\n' "$metadata"
fi
