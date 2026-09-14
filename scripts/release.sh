#!/usr/bin/env bash
# Release a named version: changelog section, release commit, tag, push, GitHub
# release. semantic-release cannot be handed a version, so this is the path
# for "make release VERSION=v1.2.3"; without a version, make release hands the
# decision to semantic-release in CI.
#
#   scripts/release.sh v1.2.3            release it
#   scripts/release.sh v1.2.3 --dry-run  print the notes and the plan only
#
# If the tag already exists (someone ran make tag), it must be reachable from
# HEAD. The tag is left where it is, because proxy.golang.org may already hold
# that version, and the changelog entry plus the GitHub release are filled in
# behind it.
set -euo pipefail

usage() { echo "usage: scripts/release.sh vX.Y.Z [--dry-run]" >&2; exit 2; }

[ $# -ge 1 ] || usage
raw=$1; shift
dry_run=false
for arg in "$@"; do
  case $arg in
    --dry-run) dry_run=true ;;
    *) usage ;;
  esac
done

bare=${raw#v}
if ! [[ $bare =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: '$raw' is not a semantic version (expected vX.Y.Z or vX.Y.Z-pre)" >&2
  exit 1
fi
tag="v$bare"

red=$'\033[31m'; green=$'\033[32m'; yellow=$'\033[33m'; blue=$'\033[34m'; reset=$'\033[0m'
say() { echo "${green}$*${reset}"; }
warn() { echo "${yellow}$*${reset}"; }
die() { echo "${red}error: $*${reset}" >&2; exit 1; }

# Compare two X.Y.Z versions without relying on sort -V, which the macOS sort
# does not have. Prints -1, 0 or 1.
vercmp() {
  local a=${1%%-*} b=${2%%-*}
  local IFS=.
  local -a pa=($a) pb=($b)
  for i in 0 1 2; do
    if (( ${pa[i]:-0} < ${pb[i]:-0} )); then echo -1; return; fi
    if (( ${pa[i]:-0} > ${pb[i]:-0} )); then echo 1; return; fi
  done
  echo 0
}

repo_url=$(git remote get-url origin | sed -E 's#^git@github\.com:#https://github.com/#; s#\.git$##')
branch=$(git branch --show-current)
git fetch -q origin main --tags
if $dry_run; then
  [ "$branch" = "main" ] || warn "not on main (on '$branch'); a real run refuses this"
  git diff-index --quiet HEAD -- || warn "working tree is not clean; a real run refuses this"
  [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || warn "main is not in sync with origin/main; a real run refuses this"
else
  [ "$branch" = "main" ] || die "release from main, not '$branch'"
  git diff-index --quiet HEAD -- || die "working tree is not clean; commit or stash first"
  [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || die "main is not in sync with origin/main; pull or push first"
fi

backfill=false
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  git merge-base --is-ancestor "$tag" HEAD || die "tag $tag exists but is not reachable from HEAD"
  backfill=true
  prev=$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)
  range="${prev:+$prev..}$tag"
  warn "tag $tag already exists at $(git rev-parse --short "$tag^{commit}"); it stays put and gets its changelog entry and GitHub release"
else
  prev=$(git describe --tags --abbrev=0 2>/dev/null || true)
  if [ -n "$prev" ] && [ "$(vercmp "$bare" "${prev#v}")" != 1 ]; then
    die "$tag is not newer than the latest tag $prev"
  fi
  range="${prev:+$prev..}HEAD"
fi
[ -n "$(git log --format=%h "$range" -- 2>/dev/null)" ] || die "no commits in $range to release"
if [ -f CHANGELOG.md ] && grep -qE "^## \[?${bare//./\\.}[]( ]" CHANGELOG.md; then
  die "$bare is already in CHANGELOG.md; if only the GitHub release is missing, create it with: gh release create $tag --title $tag --notes-file <section>"
fi

# Build the changelog section the way the conventionalcommits preset does:
# one heading with a compare link, then one section per visible commit type,
# with the release's own chore(release) commits and the hidden types left out.
date=$(date -u +%Y-%m-%d)
if [ -n "$prev" ]; then
  heading="## [$bare]($repo_url/compare/$prev...$tag) ($date)"
else
  heading="## $bare ($date)"
fi

# type(scope)!: subject, with the scope and the bang optional. Kept in a
# variable because bash misparses the bracket expression inline.
conventional='^([a-z]+)(\(([^)]+)\))?(!)?: (.+)$'
declare -a feat fix perf revert docs refactor breaking
# One git call per commit keeps this portable: the macOS tr has no hex escapes
# and bash 3.2 has no mapfile, so a single formatted log is more trouble than
# it is worth for the handful of commits a release carries.
for sha in $(git rev-list --reverse "$range" --); do
  subject=$(git log -1 --format=%s "$sha")
  body=$(git log -1 --format=%b "$sha")
  short=${sha:0:7}
  if [[ $subject =~ $conventional ]]; then
    type=${BASH_REMATCH[1]}; scope=${BASH_REMATCH[3]}; bang=${BASH_REMATCH[4]}; text=${BASH_REMATCH[5]}
  else
    type=""; scope=""; bang=""; text=$subject
  fi
  [ "$type" = "chore" ] && [ "$scope" = "release" ] && continue
  line="* ${scope:+**$scope:** }$text ([$short]($repo_url/commit/$sha))"
  if [ -n "$bang" ] || [[ $body == *"BREAKING CHANGE"* ]]; then breaking+=("$line"); fi
  case $type in
    feat) feat+=("$line") ;;
    fix) fix+=("$line") ;;
    perf) perf+=("$line") ;;
    revert) revert+=("$line") ;;
    docs) docs+=("$line") ;;
    refactor) refactor+=("$line") ;;
    *) ;;  # test, build, ci, chore and unparsable subjects stay out of the notes
  esac
done

section() {
  local title=$1; shift
  [ $# -gt 0 ] || return 0
  printf '\n### %s\n\n' "$title"
  printf '%s\n' "$@"
}
notes=$(
  echo "$heading"
  section "⚠ BREAKING CHANGES" "${breaking[@]+"${breaking[@]}"}"
  section "Features" "${feat[@]+"${feat[@]}"}"
  section "Bug Fixes" "${fix[@]+"${fix[@]}"}"
  section "Performance Improvements" "${perf[@]+"${perf[@]}"}"
  section "Reverts" "${revert[@]+"${revert[@]}"}"
  section "Documentation" "${docs[@]+"${docs[@]}"}"
  section "Code Refactoring" "${refactor[@]+"${refactor[@]}"}"
)
if [ "$(printf '%s\n' "$notes" | wc -l | tr -d ' ')" -le 1 ]; then
  warn "every commit in $range is a hidden type (test, build, ci, chore); the notes carry only the heading"
fi

echo "${blue}Release $tag${reset}  (previous: ${prev:-none}, range: $range)"
echo
printf '%s\n' "$notes"
echo
if $dry_run; then
  say "dry run: nothing written. A real run would:"
  echo "  - prepend the section above to CHANGELOG.md and commit 'chore(release): $bare [skip ci]'"
  $backfill || echo "  - tag that commit $tag"
  if $backfill; then echo "  - push main to origin (the tag is already there)"; else echo "  - push main and the tag to origin"; fi
  echo "  - create the GitHub release $tag from the section"
  exit 0
fi

command -v gh >/dev/null 2>&1 || die "GitHub CLI (gh) is required to create the release; brew install gh"

tmp=$(mktemp)
{ printf '%s\n\n' "$notes"; [ -f CHANGELOG.md ] && cat CHANGELOG.md; } > "$tmp"
mv "$tmp" CHANGELOG.md
git add CHANGELOG.md
git commit -q -m "chore(release): $bare [skip ci]"
say "committed the changelog entry for $bare"

if ! $backfill; then
  git tag -a "$tag" -m "Release $tag"
  say "tagged $tag"
fi

git push -q origin main
$backfill || git push -q origin "$tag"
if $backfill; then say "pushed main to origin (tag $tag was already there)"; else say "pushed main and $tag to origin"; fi

if gh release view "$tag" >/dev/null 2>&1; then
  warn "GitHub release $tag already exists; left as is"
else
  notes_file=$(mktemp)
  printf '%s\n' "$notes" | tail -n +2 > "$notes_file"
  gh release create "$tag" --title "$tag" --notes-file "$notes_file"
  rm -f "$notes_file"
  say "created GitHub release $tag"
fi
say "done: $repo_url/releases/tag/$tag"
