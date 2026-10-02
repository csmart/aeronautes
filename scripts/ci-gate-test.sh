#!/usr/bin/env bash
set -euo pipefail

gate="$(dirname "$0")/ci-gate.sh"
"$gate" success success success success

refuse() {
  if "$gate" "$@" >/dev/null 2>&1; then
    echo "gate accepted incomplete CI: $*" >&2
    exit 1
  fi
}

for index in 0 1 2 3; do
  for result in failure cancelled skipped ''; do
    results=(success success success success)
    results[index]="$result"
    refuse "${results[@]}"
  done
done
refuse
refuse success success success
refuse success success success success success
echo 'CI gate accepted success and refused 19 incomplete results'
