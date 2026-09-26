#!/bin/sh
# Writes release notes for TAG to stdout: how to install it, then the commits
# since the previous tag grouped by their conventional-commit type.
#
#   .github/scripts/release-notes.sh v0.2.0 > notes.md
set -eu

tag="$1"
prev="$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)"
range="$tag"
[ -n "$prev" ] && range="$prev..$tag"

section() {
	title="$1"
	pattern="$2"
	lines="$(git log --no-merges --reverse --format='%s' "$range" | grep -E "$pattern" |
		sed -E 's/^[a-z]+(\([^)]*\))?!?: //' | sed 's/^/- /' || true)"
	if [ -n "$lines" ]; then
		printf '### %s\n\n%s\n\n' "$title" "$lines"
	fi
}

cat <<NOTES
Install or update on Linux or macOS:

\`\`\`sh
curl -fsSL https://sonarc-dev.github.io/sonarc/install.sh | sh
\`\`\`

NOTES

section "Features" '^feat(\(.*\))?!?:'
section "Fixes" '^fix(\(.*\))?!?:'
section "Performance" '^perf(\(.*\))?!?:'
section "Documentation" '^docs(\(.*\))?!?:'
section "Other changes" '^(refactor|build|ci|chore|test|style)(\(.*\))?!?:'

if [ -n "$prev" ]; then
	printf '**Full changelog:** https://github.com/%s/compare/%s...%s\n' "${GITHUB_REPOSITORY:-sonarc-dev/sonarc}" "$prev" "$tag"
fi
