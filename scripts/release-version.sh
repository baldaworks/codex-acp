#!/usr/bin/env bash
set -euo pipefail

release_ref="${1:-}"
if [[ ! "$release_ref" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Expected a vX.Y.Z release tag for the root Go module" >&2
  exit 1
fi
tag_commit="$(git rev-parse "refs/tags/$release_ref^{commit}")"
head_commit="$(git rev-parse HEAD)"
if [[ "$tag_commit" != "$head_commit" ]]; then
  echo "Release tag $release_ref must point to this checkout" >&2
  exit 1
fi
printf '%s\n' "${release_ref#v}"
