#!/usr/bin/env bash
set -euo pipefail

# Tags HEAD as a snapshot prerelease and prints the push command. The base is
# the next patch after the last stable tag so the snapshot sorts above it in
# semver. The "g" keeps an all-digit short sha from being an invalid numeric
# identifier.

PR=""
REMOTE=""

usage() {
  cat <<EOF
Tag HEAD as a snapshot prerelease and print the push command.

  vX.Y.<Z+1>-pr.<N>.g<sha>   with a PR
  vX.Y.<Z+1>-dev.g<sha>      without

Usage:
  $(basename "$0") [flags]

Flags:
  -p, --pr         pull request number (default: detected with gh)
  -r, --remote     remote to fetch tags from and push to (default: the remote
                   whose GitHub repo is not a fork)
  -h, --help       show this help
EOF
}

# Prints the remote whose GitHub repo is not a fork.
upstream_remote() {
  local remote url repo found=""
  for remote in $(git remote); do
    url="$(git remote get-url "$remote")"
    [[ "$url" =~ github\.com[:/]([^/]+/[^/]+)$ ]] || continue
    repo="${BASH_REMATCH[1]%.git}"
    if [[ "$(gh api "repos/${repo}" --jq .fork)" == "false" ]]; then
      [[ -z "$found" ]] || { echo "error: remotes ${found} and ${remote} are both non-fork repos; pass --remote" >&2; return 1; }
      found="$remote"
    fi
  done
  [[ -n "$found" ]] || { echo "error: no remote points at a non-fork GitHub repo; pass --remote" >&2; return 1; }
  echo "$found"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -p|--pr)
      [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 1; }
      PR="$2"
      shift 2
      ;;
    -r|--remote)
      [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 1; }
      REMOTE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unexpected argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

git diff --quiet HEAD || { echo "error: working tree is dirty" >&2; exit 1; }

[[ -n "$PR" ]] || PR="$(gh pr view --json number -q .number 2>/dev/null || true)"
if [[ -n "$PR" && ! "$PR" =~ ^[1-9][0-9]*$ ]]; then
  echo "error: pr \"${PR}\" is not a number" >&2
  exit 1
fi

[[ -n "$REMOTE" ]] || REMOTE="$(upstream_remote)"
git fetch --tags --quiet "$REMOTE"

base="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' HEAD)"
if [[ ! "$base" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "error: expected a vX.Y.Z base tag, got ${base}" >&2
  exit 1
fi

label="dev"
[[ -z "$PR" ]] || label="pr.${PR}"
sha="$(git rev-parse HEAD)"
tag="v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((BASH_REMATCH[3] + 1))-${label}.g${sha:0:7}"

git tag "$tag" HEAD
echo "git push ${REMOTE} refs/tags/${tag}"
