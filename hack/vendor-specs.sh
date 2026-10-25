#!/usr/bin/env bash
# Re-vendors the pruned OpenAPI specs from the private platform repo.
#
# Usage: hack/vendor-specs.sh [REF]
#
# REF defaults to the ref in internal/api/specs/allowlist.yaml. Needs `gh`
# authenticated with read access to the platform repo. Only the files the
# allowlist names are fetched; hack/specprune decides what is published.
set -euo pipefail

cd "$(dirname "$0")/.."
allowlist=internal/api/specs/allowlist.yaml
repo=$(awk '/^  repo:/ {print $2; exit}' "$allowlist")
current=$(awk '/^  ref:/ {print $2; exit}' "$allowlist")
ref=${1:-$current}

if [[ "$ref" != "$current" ]]; then
  # Pin the new ref in the allowlist so the vendored headers record it.
  sed -i.bak "s/^  ref: .*/  ref: $ref/" "$allowlist" && rm -f "$allowlist.bak"
fi

src=$(mktemp -d)
trap 'rm -rf "$src"' EXIT

grep -E '^    path: ' "$allowlist" | awk '{print $2}' | while read -r path; do
  mkdir -p "$src/$(dirname "$path")"
  gh api "repos/$repo/contents/$path?ref=$ref" -H "Accept: application/vnd.github.raw" >"$src/$path"
done

go run ./hack/specprune -allowlist "$allowlist" -src "$src" -out internal/api/specs
echo "Vendored specs from $repo@$ref. Review the diff before committing: this repo is public."
