#!/usr/bin/env bash
# coverage-badge.sh <label> <percent> <soft-floor> <out.svg>
# Generates a small shields-style SVG locally (no external service —
# the repo is private, so badges live in-repo and README references
# them by relative path). Color: red below the soft floor, yellow
# within 5 points above it, green beyond.
set -euo pipefail
LABEL="${1:?label}"
PCT="${2:?percent}"
FLOOR="${3:?soft floor}"
OUT="${4:?out.svg}"

color="#e05d44" # red
awk -v p="$PCT" -v f="$FLOOR" 'BEGIN{exit !(p>=f)}' && color="#dfb317" # yellow
awk -v p="$PCT" -v f="$FLOOR" 'BEGIN{exit !(p>=f+5)}' && color="#4c1"  # green

VALUE="${PCT}%"
LW=$(( ${#LABEL} * 7 + 10 ))
VW=$(( ${#VALUE} * 7 + 10 ))
W=$(( LW + VW ))
mkdir -p "$(dirname "$OUT")"
cat > "$OUT" << SVG
<svg xmlns="http://www.w3.org/2000/svg" width="$W" height="20" role="img" aria-label="$LABEL: $VALUE">
  <linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
  <clipPath id="r"><rect width="$W" height="20" rx="3" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="$LW" height="20" fill="#555"/>
    <rect x="$LW" width="$VW" height="20" fill="$color"/>
    <rect width="$W" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
    <text x="$(( LW / 2 ))" y="14">$LABEL</text>
    <text x="$(( LW + VW / 2 ))" y="14">$VALUE</text>
  </g>
</svg>
SVG
echo "badge: $OUT ($LABEL $VALUE, floor $FLOOR)"
