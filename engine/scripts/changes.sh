#!/bin/sh
# Classifies a change for CI, from BASE (a commit) to HEAD.
#
#   changes.sh docs-only BASE   exit 0 when the change touches only files that
#                               no build, test or lint reads; exit 1 otherwise
#   changes.sh prepare BASE     exit 0 when CHANGELOG.md gains a version
#                               heading such as "## [0.15.0] - 2026-10-12";
#                               exit 1 otherwise
#
# Exit 2 on bad usage. A change with no files, or a BASE git cannot compare, is
# neither docs-only nor a Prepare change. This is the one place that rule lives.
set -eu

usage() {
	echo "usage: $0 docs-only|prepare BASE" >&2
	exit 2
}

[ $# -eq 2 ] || usage
cmd=$1
base=$2

prepare() {
	git diff --no-renames -U0 "$base" HEAD -- CHANGELOG.md |
		grep -qE '^\+## \[[0-9]+\.[0-9]+\.[0-9]+\]'
}

# Docs-only files: AGENTS.md, docs/agents/ and docs/style/, which no test reads
# (TestNoFactoryAddresses skips the agents and style folders; keep the two
# lists aligned). Every other file is read by a test or a build, whatever its
# name or type: TestNoFactoryAddresses scans every file, README.md and
# CHANGELOG.md have tests of their own, and tests read test data.
docs_only() {
	files=$(git diff --no-renames --name-only "$base" HEAD)
	[ -n "$files" ] || return 1
	! printf '%s\n' "$files" | grep -qvE '^AGENTS\.md$|^docs/(agents|style)/'
}

case $cmd in
docs-only) docs_only ;;
prepare) prepare ;;
*) usage ;;
esac
