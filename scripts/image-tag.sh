#!/usr/bin/env bash
# Image tag = short git commit, with "-dirty" if the working tree has changes.
cd "$(dirname "$0")/.."
t=$(git rev-parse --short HEAD 2>/dev/null || echo dev)
[ -n "$(git status --porcelain 2>/dev/null)" ] && t="$t-dirty"
echo "$t"
