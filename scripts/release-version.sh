#!/usr/bin/env bash
set -euo pipefail

# Fork release tags have their own namespace so upstream tags remain untouched.
release_ref="${1:-}"
upstream_tag="${release_ref#codex-acp/}"
if [[ ! "$upstream_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Expected an upstream vX.Y.Z tag or codex-acp/vX.Y.Z release tag" >&2
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

printf '%s\n' "${upstream_tag#v}"
