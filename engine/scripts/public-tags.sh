#!/bin/sh
# Compares testdata/public-core-tags.txt with the core-v tags of the public
# repository. A release step: it needs the network and is not run by make check
# or go test.
#
#   public-tags.sh [--append] [REMOTE]
#                             REMOTE is a repository URL or path; it defaults to
#                             https://github.com/JairJosafath/cel-lens/
#
# Prints "not public: <tag>" for each listed tag the repository lacks and
# "not listed: <tag>" for each core-v tag it has that the list lacks. Exit 0
# when the two agree, 1 when they differ or the remote can't be read, 2 on bad
# usage. Run it from the repository root.
#
# Without --append the script changes no file. With --append it adds each
# public tag the list lacks to the end of the list, in version order, and
# prints "appended: <tag>" instead of "not listed: <tag>"; the exit status then
# reflects only the listed tags that are not public.
set -eu

usage() {
	echo "usage: $0 [--append] [REMOTE]" >&2
	exit 2
}
append=
remote=
for arg in "$@"; do
	case $arg in
	--append) append=1 ;;
	-*) usage ;;
	*) [ -z "$remote" ] || usage
		remote=$arg ;;
	esac
done
remote=${remote:-https://github.com/JairJosafath/cel-lens/}
list=testdata/public-core-tags.txt

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git ls-remote --tags "$remote" |
	sed -n 's|^.*[[:space:]]refs/tags/\(core-v[^[:space:]^]*\)$|\1|p' |
	sort -u >"$tmp/public"
grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$list" | sort -u >"$tmp/listed" || true

comm -13 "$tmp/public" "$tmp/listed" | sed 's/^/not public: /'
comm -23 "$tmp/public" "$tmp/listed" >"$tmp/missing"
if [ -n "$append" ]; then
	sort -V "$tmp/missing" >"$tmp/new"
	if [ -s "$tmp/new" ]; then
		[ -z "$(tail -c 1 "$list")" ] || echo >>"$list"
		cat "$tmp/new" >>"$list"
		sed 's/^/appended: /' "$tmp/new"
	fi
	: >"$tmp/missing"
else
	sed 's/^/not listed: /' "$tmp/missing"
fi
[ "$(comm -23 "$tmp/listed" "$tmp/public" | wc -l)" -eq 0 ] && [ ! -s "$tmp/missing" ]
