#!/usr/bin/env bash
# Fixtures for wails-version-coherence.sh, run against that exact script rather
# than a copy of it. There are 25, and more than half describe trees some version of
# that check passed, which is why they are written down rather than reasoned about.
#
# Two of these exist because the first version of that checker passed them. With
# all five values unparsable it compared five empty strings and reported agreement,
# and a comment naming one version above an install pin naming another certified
# the comment. A checker that can pass while reading nothing is worse than no
# checker, so those two cases are kept first.
set -euo pipefail
checker="$(cd "$(dirname "$0")" && pwd)/wails-version-coherence.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

tree() { # tree <name> <module> <runtime> <makefile> <ci> <release>
  local d="$work/$1"; mkdir -p "$d/frontend" "$d/.github/workflows"
  printf 'module github.com/csmart/aeronautes\n\nrequire (\n\tgithub.com/wailsapp/wails/v3 %s\n)\n' "$2" > "$d/go.mod"
  printf '{\n  "dependencies": {\n    "@wailsio/runtime": "%s"\n  }\n}\n' "$3" > "$d/frontend/package.json"
  printf 'WAILS3_VERSION := %s\n' "$4" > "$d/Makefile"
  printf 'jobs:\n  go:\n    steps:\n      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@%s\n' "$5" > "$d/.github/workflows/ci.yml"
  printf 'jobs:\n  release:\n    steps:\n      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@%s\n' "$6" > "$d/.github/workflows/release.yml"
  printf '%s' "$d"
}

expect() { # expect <want-exit> <description> <dir>
  local want="$1" what="$2" dir="$3" got=0 out
  out=$("$checker" "$dir" 2>&1) || got=$?
  if [ "$got" -ne "$want" ]; then
    printf 'FAIL %s: exit %d, want %d\n%s\n' "$what" "$got" "$want" "$out" >&2
    failures=$((failures + 1))
    return
  fi
  printf 'ok   %s (exit %d)\n' "$what" "$got"
}

v=v3.1.0
expect 0 "all five agree" "$(tree agree "$v" 3.1.0 "$v" "$v" "$v")"

# The ordinary drift cases, one per owner.
expect 1 "runtime behind"  "$(tree r "$v" 3.0.0 "$v" "$v" "$v")"
expect 1 "makefile behind" "$(tree m "$v" 3.1.0 v3.0.0 "$v" "$v")"
expect 1 "ci behind"       "$(tree c "$v" 3.1.0 "$v" v3.0.0 "$v")"
expect 1 "release behind"  "$(tree l "$v" 3.1.0 "$v" "$v" v3.0.0)"

# The two the first checker passed.
d=$(tree unparsable "$v" 3.1.0 "$v" "$v" "$v")
for f in go.mod frontend/package.json Makefile .github/workflows/ci.yml .github/workflows/release.yml; do
  printf 'nothing this script can parse\n' > "$d/$f"
done
expect 1 "every value unparsable" "$d"

d=$(tree missing "$v" 3.1.0 "$v" "$v" "$v")
rm -f "$d/go.mod" "$d/frontend/package.json" "$d/Makefile" \
      "$d/.github/workflows/ci.yml" "$d/.github/workflows/release.yml"
expect 1 "every owning file missing" "$d"

# A comment naming the agreeing version above a pin that does not.
d=$(tree comment "$v" 3.1.0 "$v" "$v" "$v")
# The comment carries the wails3@ token itself, which is the form that shadowed the
# live pin: a checker selecting the first line mentioning wails3@ reads the comment.
printf 'jobs:\n  go:\n    steps:\n      # was wails3@v3.1.0 before the bump\n      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.2.0\n' \
  > "$d/.github/workflows/ci.yml"
expect 1 "a comment cannot certify a live pin" "$d"

# A longer token that ends in the agreeing one.
d=$(tree suffix "$v" 3.1.0 "$v" v3.1.0.1 "$v")
expect 1 "an incomplete token is refused rather than truncated" "$d"

# A later assignment the first one would hide.
d=$(tree duplicate "$v" 3.1.0 "$v" "$v" "$v")
printf 'WAILS3_VERSION := v3.1.0\nWAILS3_VERSION := v3.2.0\n' > "$d/Makefile"
expect 1 "a second assignment is refused rather than ignored" "$d"

# A file that still parses but no longer carries the owning line.
d=$(tree reshaped "$v" 3.1.0 "$v" "$v" "$v")
printf 'WAILS3_TOOL := v3.1.0\n' > "$d/Makefile"
expect 1 "a renamed owner is a drift, not agreement" "$d"

# The four a review found, each of which the previous version certified as agreeing.
# They share one cause: that version read a version-shaped substring from an
# owner-selected line instead of the value the line actually assigns.
d=$(tree live_main "$v" 3.1.0 "$v" "$v" "$v")
printf 'jobs:\n  go:\n    steps:\n      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@main # previously v3.1.0\n' \
  > "$d/.github/workflows/ci.yml"
expect 1 "a trailing comment cannot certify a live branch pin" "$d"

d=$(tree alpha_suffix "$v" 3.1.0 "$v" v3.1.0suffix "$v")
expect 1 "an alphabetic suffix is refused rather than trimmed" "$d"

d=$(tree hyphen_suffix "$v" 3.1.0 "$v" v3.1.0-nightly.20261028 "$v")
expect 1 "a prerelease suffix is refused rather than trimmed" "$d"

d=$(tree caret_range "$v" ^3.1.0 "$v" "$v" "$v")
expect 1 "a range operator is refused rather than ignored" "$d"

# The two a review found. Both turn on where a comment is removed and what is
# counted: a greedy match preferred the comment's copy of the value over the live
# argument, and the ambiguity guard counted lines rather than assignments.
d=$(tree full_pin_comment "$v" 3.1.0 "$v" "$v" "$v")
printf 'jobs:\n  go:\n    steps:\n      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.2.0 # previously wails3@v3.1.0\n' \
  > "$d/.github/workflows/ci.yml"
expect 1 "a comment repeating the whole marker cannot certify a live pin" "$d"

d=$(tree two_installs "$v" 3.1.0 "$v" "$v" "$v")
printf 'jobs:\n  go:\n    steps:\n      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.2.0 && go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0\n' \
  > "$d/.github/workflows/ci.yml"
expect 1 "two active assignments on one line are ambiguous" "$d"

# JSON has no comment syntax, so a # inside a string value is data. An
# earlier version stripped at it and lost the closing quote with it, then accepted
# what was left.
d=$(tree json_hash "$v" '3.1.0#suffix' "$v" "$v" "$v")
expect 1 "a hash inside a json value is data, not a comment" "$d"

# And an unterminated string on the line is not a value at all.
d=$(tree json_unterminated "$v" 3.1.0 "$v" "$v" "$v")
printf '{\n  "dependencies": {\n    "@wailsio/runtime": "3.1.0\n  }\n}\n' > "$d/frontend/package.json"
expect 1 "an unterminated json string is refused" "$d"

# A hash attached to a token with no preceding whitespace is not a comment
# in YAML and is never one in go.mod. A boolean comment flag truncated all three of
# these into a valid pin.
d=$(tree ci_attached "$v" 3.1.0 "$v" 'v3.1.0#suffix' "$v")
expect 1 "an attached hash in a ci install argument is part of the token" "$d"

d=$(tree release_attached "$v" 3.1.0 "$v" "$v" 'v3.1.0#suffix')
expect 1 "an attached hash in a release install argument is part of the token" "$d"

d=$(tree module_attached 'v3.1.0#suffix' 3.1.0 "$v" "$v" "$v")
expect 1 "a hash is never a comment in go.mod" "$d"

# The real comment boundaries each format does have, which must keep working.
d=$(tree make_comment "$v" 3.1.0 "$v" "$v" "$v")
printf 'WAILS3_VERSION := v3.1.0 # bumped with the module\n' > "$d/Makefile"
expect 0 "a genuine make comment is still stripped" "$d"

d=$(tree yaml_comment "$v" 3.1.0 "$v" "$v" "$v")
printf 'jobs:\n  go:\n    steps:\n      - run: go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 # matches the module\n' \
  > "$d/.github/workflows/ci.yml"
expect 0 "a genuine yaml comment, after whitespace, is still stripped" "$d"

d=$(tree module_comment "$v" 3.1.0 "$v" "$v" "$v")
printf 'module x\n\nrequire (\n\tgithub.com/wailsapp/wails/v3 v3.1.0 // indirect\n)\n' > "$d/go.mod"
expect 0 "a go.mod // comment is stripped" "$d"

if [ "$failures" -ne 0 ]; then
  printf '%d case(s) failed\n' "$failures" >&2
  exit 1
fi
printf 'all coherence fixtures behaved\n'
