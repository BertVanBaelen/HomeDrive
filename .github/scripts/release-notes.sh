#!/usr/bin/env bash
# Prints the add-on version from homedrive/config.yaml. With a file name,
# also writes that version's section of homedrive/CHANGELOG.md to it: the
# lines under "## <version>" up to the next "## " heading.
#
# Fails if the version isn't X.Y.Z or the changelog has no notes for it, so
# CI catches a version bump without release notes before it reaches main.
set -euo pipefail
cd "$(dirname "$0")/../.."

version="$(sed -nE 's/^version: *"?([^" ]+)"? *$/\1/p' homedrive/config.yaml)"
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "homedrive/config.yaml: version \"$version\" is not X.Y.Z" >&2
  exit 1
fi

notes="$(awk -v v="$version" '
  /^## / { if (found) exit; found = ($2 == v); next }
  found
' homedrive/CHANGELOG.md | sed '/[^[:space:]]/,$!d')"
if [ -z "$notes" ]; then
  echo "homedrive/CHANGELOG.md: no notes under \"## $version\"" >&2
  exit 1
fi

if [ $# -gt 0 ]; then
  printf '%s\n' "$notes" > "$1"
fi
echo "$version"
