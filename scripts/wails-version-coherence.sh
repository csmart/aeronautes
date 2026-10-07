#!/usr/bin/env bash
# Every place a Wails version is pinned has to say the same thing.
#
# Five values exist and three are `go install` arguments, which no dependency bot
# can see: the Go module and the JS runtime move when a bot moves them, while the
# CLI pins move only when somebody remembers. On 7 October 2026 they had drifted to
# three different versions and the release workflow was building the published
# artifact with a CLI four betas behind the module it compiled.
#
# Usage: wails-version-coherence.sh [root]
#
# The optional root lets the fixtures in wails-version-coherence-test.sh exercise
# this exact script against trees built to fail, rather than a copy of it.
#
# Two properties below are there because earlier versions of this script lacked
# them and passed trees that should have failed. It must not pass while any value
# failed to parse, and it must read the value a file actually OWNS rather than any
# version-shaped text on the same line. The second is why extraction yields a
# complete token which is then matched anchored: searching a line for a version
# certified a comment while the live install argument said something else.
set -euo pipefail
root=${1:-"$(dirname "$0")/.."}
cd "$root"

status=0
complain() { printf '%s\n' "coherence: $*" >&2; status=1; }

# Collected in the calling shell. A helper called inside $(...) runs in a subshell
# and anything it records about failure dies with it; an earlier version did that
# and compared five empty strings, found them equal, and passed.
declare -A version=()

# The only accepted spelling of a pin, anchored. The Go module and the install
# arguments carry the leading v; the npm runtime does not. Anything else — a branch
# name, a prerelease suffix, a range operator, a trailing comment — fails here
# rather than being pattern-matched out of a longer string.
readonly exact='^v?3\.0\.0-beta\.[0-9]+$'

# Comment syntax is a property of each format and they do not agree, so one
# stripper for all of them is wrong in both directions. A boolean "has comments"
# flag truncated at any hash, which turned v3.0.0-beta.27#suffix — a complete token
# that is not a pin — into a valid pin in go.mod, in YAML and in the install
# arguments.
#
#   none   JSON: no comment syntax at all, so a hash is data.
#   hash   Make: a hash begins a comment anywhere on the line.
#   yaml   YAML: a hash begins a comment only at the start of the line or after
#          whitespace; attached to a token it is part of the scalar.
#   slash  go.mod: comments are //, and a hash is never one.
strip_comment() { # strip_comment <line> <style>
  case "$2" in
    none)  printf '%s' "$1" ;;
    hash)  printf '%s' "${1%%#*}" ;;
    yaml)  printf '%s' "$1" | sed -E 's/(^|[[:space:]])#.*$/\1/' ;;
    slash) printf '%s' "${1%%//*}" ;;
    *)     printf '%s' "$1" ;;
  esac
}
trim() { local s="$1"; s="${s#"${s%%[![:space:]]*}"}"; printf '%s' "${s%"${s##*[![:space:]]}"}"; }

# occurrences <line> <marker> — how many times the owned marker appears. The guard
# below requires exactly one, because two live assignments on one line are
# ambiguous and an earlier version counted lines rather than assignments.
occurrences() {
  local rest="$1" marker="$2" n=0
  while [ "${rest#*"$marker"}" != "$rest" ]; do
    rest=${rest#*"$marker"}
    n=$((n + 1))
  done
  printf '%s' "$n"
}

# owning_lines <file> <pattern> — active lines (whole-line comments dropped) that
# are allowed to carry this value. Exactly one must.
owning_lines() { sed -e 's/\r$//' -e '/^[[:space:]]*#/d' "$1" | grep -E "$2" || true; }

# record <key> <file> <pattern> <marker> <comments> <extractor>
# The extractor turns one owning line into the complete value that line assigns. It
# is only called once exactly one occurrence of the marker remains in the line.
#
# <style> names the owning format's comment syntax, because stripping is
# destructive and the formats disagree about where a comment starts.
record() {
  local key="$1" file="$2" pattern="$3" marker="$4" style="$5" extractor="$6" lines count value owned
  if [ ! -f "$file" ]; then
    complain "$key: $file is missing"
    return
  fi
  lines=$(owning_lines "$file" "$pattern")
  if [ -z "$lines" ]; then
    complain "$key: no line in $file matches the pattern that owns this value; the file changed shape, which is itself a drift"
    return
  fi
  count=$(printf '%s\n' "$lines" | grep -c .)
  if [ "$count" -ne 1 ]; then
    complain "$key: $count lines in $file carry this value and only one may; a later assignment can silently override an earlier one"
    return
  fi
  lines=$(strip_comment "$lines" "$style")
  owned=$(occurrences "$lines" "$marker")
  if [ "$owned" -ne 1 ]; then
    complain "$key: the line in $file carries $owned active \"$marker\" assignments and exactly one is required; two on one line are ambiguous and none means the value lives only in a comment"
    return
  fi
  value=$("$extractor" "$lines")
  if [ -z "$value" ]; then
    complain "$key: the line in $file assigns no value this check can read: $(trim "$lines")"
    return
  fi
  if ! printf '%s' "$value" | grep -qE "$exact"; then
    complain "$key: $file pins \"$value\", which is not an exact 3.0.0-beta.<n> version; ranges, branches and prerelease suffixes are refused rather than interpreted"
    return
  fi
  # Compared without the leading v, a spelling difference between Go and npm.
  version["$key"]="${value#v}"
}

# One extractor per owner. Each takes the text after the FIRST occurrence of the
# marker and stops at the first whitespace or closing quote, using shortest-match
# expansion rather than a regular expression, because a greedy regex preferred the
# last occurrence on the line and that is how a comment came to supply the value.
extract_after_marker() { # extract_after_marker <line> <marker>
  local rest=${1#*"$2"}
  rest=${rest%%[[:space:]]*}
  printf '%s' "$rest"
}
extract_module()   { extract_after_marker "$(trim "$1")" 'github.com/wailsapp/wails/v3 '; }
extract_makefile() { local v=${1#*=}; trim "$v"; }
extract_install()  { extract_after_marker "$1" 'wails3@'; }
# The JSON value is whatever lies between its quotes, including any # it contains.
# The closing quote is required: without it the string is unterminated on this line
# and there is no complete value to read, which an earlier version silently treated
# as a value ending wherever the text happened to stop.
extract_runtime()  {
  local rest=${1#*\"@wailsio/runtime\"}
  rest=${rest#*:}
  case $rest in
    *\"*\"*) ;;
    *) return 0 ;;
  esac
  rest=${rest#*\"}
  printf '%s' "${rest%%\"*}"
}

#      key             file                          owning pattern                                          marker                          style  extractor
record "go module"    go.mod                        '^[[:space:]]*github\.com/wailsapp/wails/v3[[:space:]]' 'github.com/wailsapp/wails/v3 ' slash  extract_module
record "js runtime"   frontend/package.json         '"@wailsio/runtime"[[:space:]]*:'                        '"@wailsio/runtime"'            none   extract_runtime
record "makefile cli" Makefile                      '^WAILS3_VERSION[[:space:]]*[:?]?='                     'WAILS3_VERSION'                hash   extract_makefile
record "ci cli"       .github/workflows/ci.yml      'wails3@'                                               'wails3@'                       yaml   extract_install
record "release cli"  .github/workflows/release.yml 'wails3@'                                               'wails3@'                       yaml   extract_install

# Nothing is compared unless all five parsed; five empty strings are equal.
for key in "go module" "js runtime" "makefile cli" "ci cli" "release cli"; do
  [ -n "${version[$key]:-}" ] || status=1
done
if [ "$status" -ne 0 ]; then
  printf '%s\n' "coherence: refusing to compare versions that could not all be read" >&2
  exit 1
fi

printf 'wails versions: module=%s runtime=%s makefile=%s ci=%s release=%s\n' \
  "${version[go module]}" "${version[js runtime]}" "${version[makefile cli]}" \
  "${version[ci cli]}" "${version[release cli]}"

# One direction only: the module is the bot's to move, and a check requiring it to
# equal anything else would fight it.
for key in "js runtime" "makefile cli" "ci cli" "release cli"; do
  if [ "${version[$key]}" != "${version[go module]}" ]; then
    complain "$key is ${version[$key]} but the go module is ${version[go module]}"
  fi
done

if [ "$status" -ne 0 ]; then
  printf '%s\n' "coherence: every wails pin must equal the go module version; update the ones named above" >&2
  exit 1
fi
printf '%s\n' "all wails pins agree at ${version[go module]}"
