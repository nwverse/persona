#!/bin/bash
# Check that the trusted Deno adapter committed at <commit> is the one scripts/gen/deno_adapter.sh builds from that
# commit's sdk/ts/src (#6001). pre-push runs it when the pushed commits change the adapter or what it is built from;
# by hand: scripts/hooks/check-deno-adapter.sh HEAD. It builds from the commit, never the worktree, with the commit's own
# generator, and needs bun on PATH: without it the check refuses, naming the version .github/workflows/server-test.yml
# pins. See docs/operations/testing.md, "Trusted Deno adapter".
set -u
commit="${1:?usage: check-deno-adapter.sh <commit>}"
adapter=server/internal/extension/denohost/adapter.mjs
git cat-file -e "$commit:server/internal/extension/denohost/adapter.ts" 2>/dev/null || exit 0 # no adapter to check
pinned="$(git show "$commit:.github/workflows/server-test.yml" 2>/dev/null |
  sed -n "s/^[[:space:]]*bun-version:[[:space:]]*['\"]\{0,1\}\([0-9][0-9.]*\).*/\1/p" | head -1)"
pinned="${pinned:-the one .github/workflows/server-test.yml pins}"
regenerate="scripts/gen/deno_adapter.sh with Bun $pinned on PATH, then commit $adapter"
if ! command -v bun >/dev/null 2>&1; then
  echo "pre-push: these commits change the trusted Deno adapter or its sources (sdk/ts/src), and bun is not on PATH to check $adapter." >&2
  echo "  Install Bun $pinned (pinned in .github/workflows/server-test.yml), for example:" >&2
  echo "    npm install --prefix ~/.bun-$pinned bun@$pinned && export PATH=~/.bun-$pinned/node_modules/.bin:\$PATH" >&2
  echo "  then run $regenerate." >&2
  exit 1
fi
tmp="$(mktemp -d)" || exit 1
trap 'rm -rf "${tmp:?}"' EXIT
if ! git archive --format=tar "$commit" sdk/ts server/internal/extension/denohost scripts/gen/deno_adapter.sh | tar -x -C "$tmp"; then
  echo "pre-push: cannot read the trusted Deno adapter's sources from $commit" >&2
  exit 1
fi
cp "$tmp/$adapter" "$tmp/committed.mjs" || exit 1
if ! bash "$tmp/scripts/gen/deno_adapter.sh" >"$tmp/build.log" 2>&1; then
  echo "pre-push: building the trusted Deno adapter from $commit failed:" >&2
  sed 's/^/  /' "$tmp/build.log" >&2
  exit 1
fi
cmp -s "$tmp/committed.mjs" "$tmp/$adapter" && exit 0
echo "pre-push: $adapter is stale: it is not what $commit's sdk/ts/src builds. Run $regenerate." >&2
version="$(bun --version 2>/dev/null)"
if [ "$version" != "$pinned" ]; then
  echo "  (bun on PATH is ${version:-unknown}, not the pinned $pinned: build with $pinned before deciding the adapter is stale)" >&2
fi
exit 1
