#!/usr/bin/env bash
# Writes release notes for TAG (at commit SHA) to stdout: GitHub's generated notes for merged pull
# requests, plus every other commit since the previous version tag (pushed straight to main, or
# merged without a pull request).
# Usage: release-notes.sh OWNER/REPO TAG SHA. Needs gh (authenticated) and full git history.
set -euo pipefail
repo=$1 tag=$2 sha=$3

prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' "$sha^" 2>/dev/null || true)
args=(-f tag_name="$tag" -f target_commitish="$sha")
[ -n "$prev" ] && args+=(-f previous_tag_name="$prev")
body=$(gh api "repos/$repo/releases/generate-notes" "${args[@]}" --jq .body)

# Commits that reached the release through a merged pull request are already listed above.
range=$sha
[ -n "$prev" ] && range="$prev..$sha"
commits=""
while read -r c subject; do
  prs=$(gh api "repos/$repo/commits/$c/pulls" --jq '[.[] | select(.merged_at != null)] | length')
  [ "$prs" = 0 ] && commits+="* $subject (${c:0:7})"$'\n'
done < <(git log --no-merges --format='%H %s' "$range")

# GitHub's notes end with a "**Full Changelog**" link; keep that last.
changelog=$(grep -m1 '^\*\*Full Changelog\*\*' <<<"$body" || true)
main=$(grep -v '^\*\*Full Changelog\*\*' <<<"$body" | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' || true)

[ -n "$main" ] && printf '%s\n\n' "$main"
[ -n "$commits" ] && printf '## Other changes\n%s\n' "$commits"
[ -n "$changelog" ] && printf '%s\n' "$changelog"
