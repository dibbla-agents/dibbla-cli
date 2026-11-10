#!/bin/sh
# Dependency vulnerability gate for pull requests (replaces Snyk's PR check).
#
# Fails only when the PR ADDS a known vulnerability: one that osv-scanner finds
# in HEAD's lockfiles but not in the merge-base with the target branch. Findings
# that already exist on the target branch are listed as a count, never as a
# failure, so a newly published advisory does not turn every open PR red.
#
#   .github/scripts/osv-diff.sh <target-ref>     e.g. origin/master
#
# Needs: git, jq, osv-scanner (OSV_SCANNER, default `osv-scanner`). Runs locally
# too: `.github/scripts/osv-diff.sh origin/main`.
#
# Only lockfiles and go.mod are extracted from each commit (git show, no
# checkout), so both sides are scanned from identical relative paths and
# nothing untracked (node_modules, build output) is ever scanned.
#
# A false positive or an accepted risk goes in osv-scanner.toml at the repo
# root ([[IgnoredVulns]] id, ignoreUntil, reason); it is applied to both sides.
set -eu

target=${1:?usage: osv-diff.sh <target-ref>}
osv=${OSV_SCANNER:-osv-scanner}
root=$(git rev-parse --show-toplevel)
base=$(git merge-base HEAD "$target")
head=$(git rev-parse HEAD)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

locks='(^|/)(go\.mod|package-lock\.json|npm-shrinkwrap\.json|pnpm-lock\.yaml|yarn\.lock|bun\.lock|requirements[^/]*\.txt|poetry\.lock|uv\.lock|Pipfile\.lock|Cargo\.lock|Gemfile\.lock|composer\.lock)$'
skip='(^|/)(node_modules|vendor|testdata)/'

extract() { # <rev> <dir>
  mkdir -p "$2"
  git ls-tree -r --name-only "$1" | grep -E "$locks" | grep -v -E "$skip" |
    while IFS= read -r f; do
      mkdir -p "$2/$(dirname "$f")"
      git show "$1:$f" >"$2/$f"
    done
}

scan() { # <dir> <out.json>  →  one "path|ecosystem|name|version|id" per line
  set -- "$1" "$2"
  cfg=""
  [ -f "$root/osv-scanner.toml" ] && cfg="--config=$root/osv-scanner.toml"
  rc=0
  # shellcheck disable=SC2086
  "$osv" scan source -r --format json --no-resolve --no-call-analysis=all \
    --allow-no-lockfiles --verbosity error $cfg "$1" >"$2.raw" || rc=$?
  # 0 = clean, 1 = vulnerabilities found, 128 = nothing to scan. Anything
  # else is the scanner failing, and a gate must not pass on that.
  case $rc in 0 | 1 | 128) ;; *) echo "osv-scanner failed (exit $rc)" >&2; exit 2 ;; esac
  [ -s "$2.raw" ] || echo '{}' >"$2.raw"
  jq -r --arg dir "$1/" '
    .results[]? | (.source.path | ltrimstr($dir)) as $p
    | .packages[] | .package as $k
    | .vulnerabilities[] | "\($p)|\($k.ecosystem)|\($k.name)|\($k.version)|\(.id)"
  ' "$2.raw" | sort -u >"$2"
}

extract "$base" "$work/base"
extract "$head" "$work/head"
scan "$work/base" "$work/base.txt"
scan "$work/head" "$work/head.txt"

# Compare on path|ecosystem|name|id: a PR that moves a package from one
# vulnerable version to another does not add that vulnerability.
cut -d'|' -f1-3,5 "$work/base.txt" | sort -u >"$work/base.keys"
awk -F'|' 'NR==FNR { seen[$0]=1; next } !(($1"|"$2"|"$3"|"$5) in seen)' \
  "$work/base.keys" "$work/head.txt" >"$work/new.txt"

existing=$(wc -l <"$work/head.txt" | tr -d ' ')
added=$(wc -l <"$work/new.txt" | tr -d ' ')
echo "osv-scanner: $existing finding(s) in HEAD $(git rev-parse --short HEAD), $added not on merge-base $(git rev-parse --short "$base")."

[ "$added" -eq 0 ] && exit 0

echo
echo "This PR adds known vulnerabilities:"
while IFS='|' read -r path eco name version id; do
  summary=$(jq -r --arg id "$id" --arg p "$name" '
    [.results[]?.packages[] | select(.package.name == $p) | .vulnerabilities[]
     | select(.id == $id)][0]
    | (.summary // .details // "" | split("\n")[0][0:100]) + "  fixed in: "
      + ([.affected[]?.ranges[]?.events[]? | .fixed? // empty] | unique | join(", ") | if . == "" then "-" else . end)
  ' "$work/head.txt.raw")
  echo "  $path  $eco $name@$version  $id  https://osv.dev/$id"
  echo "      $summary"
done <"$work/new.txt"
echo
echo "Upgrade to a fixed version, or record an accepted risk in osv-scanner.toml."
exit 1
