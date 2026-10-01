#!/usr/bin/env bash
set -euo pipefail

# Fork release tags have their own namespace so upstream tags remain untouched.
release_ref="${1:-}"
upstream_tag="${release_ref#codex-acp/}"
if [[ ! "$upstream_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Expected an upstream vX.Y.Z tag or codex-acp/vX.Y.Z release tag" >&2
  exit 1
fi

# Fork patches can have a newer version while retaining a canonical baseline.
version="${upstream_tag#v}"
if [[ "$release_ref" == codex-acp/* ]]; then
  upstream_tag="${2:-$(cat "$(dirname "$0")/../.omnidist/upstream-tag")}"
fi
if [[ ! "$upstream_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Expected a canonical upstream vX.Y.Z baseline" >&2
  exit 1
fi
if [[ "$(printf '%s\n' "${upstream_tag#v}" "$version" | sort -V | head -n 1)" != "${upstream_tag#v}" ]]; then
  echo "Release version must not precede upstream $upstream_tag" >&2
  exit 1
fi

# Validate against the canonical upstream, rather than trusting a fork-only tag.
upstream_ref="refs/upstream-tags/$upstream_tag"
git fetch --quiet --no-tags https://github.com/normahq/codex-acp-bridge.git \
  "refs/tags/$upstream_tag:$upstream_ref"
if ! git merge-base --is-ancestor "$upstream_ref^{commit}" HEAD; then
  echo "Upstream $upstream_tag is not part of this checkout" >&2
  exit 1
fi

printf '%s\n' "$version"
