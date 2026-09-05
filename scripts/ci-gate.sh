#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo '::error::expected results for Go, frontend, SAIO and smoke' >&2
  exit 1
fi

for result in "$@"; do
  if [ "$result" != success ]; then
    echo "::error::a required CI job did not succeed (result: $result)" >&2
    exit 1
  fi
done
