#!/usr/bin/env bash
set -eu

tag=${1:?Pass a release tag such as v0.1.0}
changelog=${2:-CHANGELOG.md}
notes=$(awk -v title="## $tag" '
  /^## / {
    in_release = ($0 == title || index($0, title " · ") == 1)
    if (in_release) sections++
    next
  }
  in_release && /^- [^[:space:]]/ { print; changes++ }
  END { if (sections != 1 || changes == 0) exit 1 }
' "$changelog") || {
  printf 'Add one nonempty section for %s to %s.\n' "$tag" "$changelog" >&2
  exit 1
}
printf '%s\n' "$notes"
