#!/usr/bin/env bash
# Makes the local files the 0.5.0 test plan's steps ask for, in a folder
# you name, so no step starts by making them by hand. Nothing here touches a
# store or holds a key.
#
#   scripts/test-fixtures.sh ~/aeronautes-fixtures
#
# What it makes, each in a folder of its own:
#   small/      2,000 small files, 0000.txt to 1999.txt (steps 4a and 18k)
#   many/       10,001 empty files, over the folder upload's limit (step 13)
#   nested/     files in subfolders, a symbolic link and an empty folder (step 13)
#   large/      three 3 MiB files: set the job's chunk size to 1Mi under
#               "Per-job settings" and each uploads in segments (steps 19i, 19n, 19p, 19q)
#   replace/    three files, and replace-edited/ holding the same three with
#               one changed (steps 19t and 19u)
#   rclone.conf.template   two Swift remotes with placeholders for a test
#               account's details and no key (steps 19e and 19m)
set -euo pipefail

target=${1:?usage: scripts/test-fixtures.sh <folder to create>}
if [ -e "$target" ]; then
	echo "$target already exists; name a folder that does not, so nothing of yours is written over" >&2
	exit 1
fi
mkdir -p "$target"/{small,many,nested/a/b,nested/empty,large,replace,replace-edited}

for i in $(seq 0 1999); do
	printf 'file %04d\n' "$i" >"$target/small/$(printf '%04d' "$i").txt"
done
(cd "$target/many" && seq -f '%05g.txt' 0 10000 | xargs touch)

echo top >"$target/nested/top.txt"
echo inner >"$target/nested/a/inner.txt"
echo deeper >"$target/nested/a/b/deeper.txt"
ln -s top.txt "$target/nested/link-to-top"

for name in one two three; do
	head -c $((3 << 20)) /dev/urandom >"$target/large/$name.bin"
done

for name in alpha beta gamma; do
	echo "the $name file" >"$target/replace/$name.txt"
	cp "$target/replace/$name.txt" "$target/replace-edited/$name.txt"
done
echo "an edited line" >>"$target/replace-edited/beta.txt"

cat >"$target/rclone.conf.template" <<'EOF'
# Fill in a TEST account's details; delete this file when the steps are done.
[test-with-key]
type = swift
user = <test user>
key = <test key>
auth = <auth URL>

[test-without-key]
type = swift
user = <test user>
auth = <auth URL>
EOF

echo "made the test plan's fixtures in $target"
