#!/bin/sh
# Compares testdata/public-core-tags.txt with the core-v tags of the public
# repository. A release step: it needs the network and is not run by make check
# or go test.
#
#   public-tags.sh [REMOTE]   REMOTE is a repository URL or path; it defaults to
#                             https://github.com/JairJosafath/cel-lens/
#
# Prints "not public: <tag>" for each listed tag the repository lacks and
# "not listed: <tag>" for each core-v tag it has that the list lacks. Exit 0
# when the two agree, 1 when they differ or the remote can't be read, 2 on bad
# usage. Run it from the repository root.
set -eu

[ $# -le 1 ] || {
	echo "usage: $0 [REMOTE]" >&2
	exit 2
}
remote=${1:-https://github.com/JairJosafath/cel-lens/}
list=testdata/public-core-tags.txt

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git ls-remote --tags "$remote" |
	sed -n 's|^.*[[:space:]]refs/tags/\(core-v[^[:space:]^]*\)$|\1|p' |
	sort -u >"$tmp/public"
grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$list" | sort -u >"$tmp/listed" || true

comm -13 "$tmp/public" "$tmp/listed" | sed 's/^/not public: /'
comm -23 "$tmp/public" "$tmp/listed" | sed 's/^/not listed: /'
[ "$(comm -3 "$tmp/public" "$tmp/listed" | wc -l)" -eq 0 ]
