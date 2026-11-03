#!/bin/sh
# Prepares the release of VERSION (X.Y.Z): brings testdata/public-core-tags.txt
# up to date with the core-v tags of the public repository, then rewrites the
# [X.Y.Z]: link lines of CHANGELOG.md from that list. A release step: it needs
# the network and is not run by make check or go test. Run it from the
# repository root, through `make release RELEASE=X.Y.Z`.
#
#   prepare-release.sh VERSION
#
# The tag source is the repository at $CEL_LENS_PUBLIC_REMOTE, a URL or path
# that `git ls-remote --tags` reads; it defaults to
# https://github.com/JairJosafath/cel-lens/. Tests point it at a local repository.
#
# The tag list only gains tags: every public core-v tag it lacks is added, none
# is removed. When CHANGELOG.md has no "## [VERSION]" section, the contents of
# [Unreleased] move into one dated today (or $RELEASE_DATE, YYYY-MM-DD). Every
# version whose tag is public gets a compare link from the previous such
# version; VERSION gets a compare link to its own core-v tag, which the publish
# makes true; [Unreleased] compares the newest version to HEAD. A version whose
# tag is not public has no link, and the intro of the changelog is left alone.
#
# When the public repository can't be read (git fails, or it has no core-v tag)
# the script prints one line naming the repository, the address and the reason,
# changes no file and exits 1. Exit 2 on bad usage or a missing file.
set -eu

repo=JairJosafath/cel-lens
list=testdata/public-core-tags.txt
log=CHANGELOG.md
base=https://github.com/$repo/

[ $# -eq 1 ] && printf '%s\n' "$1" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || {
	echo "usage: $0 X.Y.Z" >&2
	exit 2
}
version=$1
remote=${CEL_LENS_PUBLIC_REMOTE:-$base}
for f in "$list" "$log"; do
	if [ ! -f "$f" ]; then
		echo "$0: $f not found; run it from the repository root" >&2
		exit 2
	fi
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# POSIX sh has no pipefail, so read the remote on its own to see it fail.
if ! git ls-remote --tags "$remote" >"$tmp/remote" 2>"$tmp/err"; then
	reason=$(tr '\n' ' ' <"$tmp/err" | sed 's/[[:space:]]*$//')
	echo "cannot read the public repository $repo at $remote: ${reason:-git ls-remote failed}" >&2
	exit 1
fi
sed -n 's|^.*[[:space:]]refs/tags/\(core-v[^[:space:]^]*\)$|\1|p' "$tmp/remote" >"$tmp/found"
if [ ! -s "$tmp/found" ]; then
	echo "cannot read the public repository $repo at $remote: it answered with no core-v tag" >&2
	exit 1
fi

# The new list: the old file, then each public tag it lacks, in version order.
grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$list" | sort -u >"$tmp/listed" || true
sort -u "$tmp/found" | comm -23 - "$tmp/listed" | sort -V >"$tmp/new"
cp "$list" "$tmp/list"
if [ -s "$tmp/new" ]; then
	[ -z "$(tail -c 1 "$tmp/list")" ] || echo >>"$tmp/list"
	cat "$tmp/new" >>"$tmp/list"
fi
grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$tmp/list" | sort -u >"$tmp/public"

# The changelog: add the section of VERSION when it is missing, then replace the link lines.
grep -q "^## \[$version\] - " "$log" && has=1 || has=
awk -v version="$version" -v date="${RELEASE_DATE:-$(date -u +%Y-%m-%d)}" -v has="$has" '
/^\[[^]]+\]: / { next }
/^## \[Unreleased\]/ && !has { print; print ""; print "## [" version "] - " date; next }
{ print }
' "$log" |
	awk 'NF { for (; blank > 0; blank--) print ""; print; next } { blank++ }' >"$tmp/body"

awk -v version="$version" -v base="$base" -v publicfile="$tmp/public" '
BEGIN {
	while ((getline tag < publicfile) > 0) pub[tag] = 1
}
/^## \[[0-9]+\.[0-9]+\.[0-9]+\]/ {
	v = $2
	gsub(/[][]/, "", v)
	versions[++n] = v
}
END {
	print ""
	if (n == 0) exit
	print "[Unreleased]: " base "compare/core-v" versions[1] "...HEAD"
	for (i = 1; i <= n; i++) {
		v = versions[i]
		if (!(("core-v" v) in pub) && v != version) continue
		from = ""
		for (j = i + 1; j <= n && from == ""; j++)
			if (("core-v" versions[j]) in pub) from = versions[j]
		if (from == "") print "[" v "]: " base "releases/tag/core-v" v
		else print "[" v "]: " base "compare/core-v" from "...core-v" v
	}
}
' "$tmp/body" >"$tmp/links"

cat "$tmp/body" "$tmp/links" >"$tmp/log"
cp "$tmp/list" "$list"
cp "$tmp/log" "$log"
sed 's/^/added to the tag list: /' "$tmp/new"
echo "prepared $version: $log links rewritten from $list"
