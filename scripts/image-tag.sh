#!/usr/bin/env bash
# Image tag = short git commit. With uncommitted changes under services/ it is
# <commit>-dirty-<hash of those changes>, so every distinct source state gets a
# distinct tag (otherwise helm sees no change and pods keep the old image).
cd "$(dirname "$0")/.."
t=$(git rev-parse --short HEAD 2>/dev/null || echo dev)
if [ -n "$(git status --porcelain -- services 2>/dev/null)" ]; then
  h=$( { git diff HEAD -- services; git ls-files --others --exclude-standard -z -- services | xargs -0 -r cat; } | sha256sum | cut -c1-8)
  t="$t-dirty-$h"
fi
echo "$t"
