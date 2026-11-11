#!/usr/bin/env bash
# The smoke driver's own process handling, checked before it is relied on.
#
# These are failures of the HARNESS, which a passing smoke cannot catch, so they
# are checked directly. Every case waits for the state it is about to test to
# exist — a group can be there before the trap or the children it is supposed to
# have — because a case that passes only on a lucky schedule proves nothing.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=scripts/app-process.sh
. scripts/app-process.sh

LOG=$(mktemp -d)
fail() { printf 'app-process: FAIL: %s\n' "$*" >&2; exit 1; }
# This shell owns launches of its own, so it gets the same cleanup it is testing,
# installed BEFORE the first one. The trap body does not change the status the
# shell exits with, so a failing case still reports its failure.
remove_logs() { rm -rf "$LOG"; }
app_install_traps remove_logs

# file_wait <path> <seconds> — a fixture's own acknowledgement that it is ready.
file_wait() {
  local waited=0
  until [ -e "$1" ]; do
    [ "$waited" -ge "$2" ] && return 1
    sleep 1
    waited=$((waited + 1))
  done
}

# members_wait <handle> <count> <seconds> — the group holds at least count.
members_wait() {
  local waited=0 seen
  while :; do
    seen=$(pgrep -g "$1" 2>/dev/null | wc -l)
    [ "$seen" -ge "$2" ] && return 0
    [ "$waited" -ge "$3" ] && return 1
    sleep 1
    waited=$((waited + 1))
  done
}

# 1. An ordinary child stops, and the stop is confirmed rather than assumed.
app_start "$LOG/plain.log" sleep 300; group=$APP_LAST_GROUP
app_owned_alive "$group" || fail "a started launch is not alive"
app_stop "$group" 10 || fail "an ordinary launch did not stop"
app_owned_alive "$group" && fail "app_stop returned success with the launch alive"

# 2. A child that IGNORES TERM must still be gone when app_stop returns. The
# fixture says when its trap is installed: an ignored disposition is inherited
# through fork and exec, so before that moment TERM would simply kill it and the
# escalation this case exists for would never be reached.
# shellcheck disable=SC2016  # $1 is the fixture shell's own argument
app_start "$LOG/stubborn.log" bash -c 'trap "" TERM; : >"$1"; sleep 300' \
  app-process "$LOG/stubborn.ready"; group=$APP_LAST_GROUP
file_wait "$LOG/stubborn.ready" 15 || fail "the TERM-resistant fixture never acknowledged its trap"
app_stop "$group" 2 || fail "a TERM-resistant launch was not escalated"
app_owned_alive "$group" && fail "a TERM-resistant launch survived app_stop"

# 3. A group whose leader has children: stopping the group stops all of them.
# The fixture acknowledges after forking both, and the group is not signalled
# until it actually holds all three.
# shellcheck disable=SC2016  # $1 is the fixture shell's own argument
app_start "$LOG/tree.log" bash -c 'sleep 300 & sleep 300 & : >"$1"; wait' \
  app-process "$LOG/tree.ready"; group=$APP_LAST_GROUP
file_wait "$LOG/tree.ready" 15 || fail "the child-bearing fixture never acknowledged its children"
members_wait "$group" 3 15 || fail "the child-bearing group never held three processes"
app_stop "$group" 10 || fail "a group with children did not stop"
app_owned_alive "$group" && fail "children survived their group's stop"

# 4. An interrupted driver: one that registers launches and is then killed by
# TERM while waiting must leave nothing behind. This is the case a review
# reproduced, and it holds the registration and the EXIT trap together.
groups=$(bash -c '
  set -euo pipefail
  . scripts/app-process.sh
  app_install_traps
  app_start /dev/null sleep 300; one=$APP_LAST_GROUP
  app_start /dev/null bash -c "trap \"\" TERM; sleep 300"; two=$APP_LAST_GROUP
  printf "%s %s\n" "$one" "$two"
  kill -TERM $$
  sleep 30
') || true
[ -n "$groups" ] || fail "the interrupted driver reported no launches"
for group in $groups; do
  for _ in $(seq 1 10); do
    app_owned_alive "$group" || break
    sleep 1
  done
  app_owned_alive "$group" && fail "launch $group outlived the interrupted driver"
done

# 5. Registration itself: every launch is in the caller's own list, which is what
# the trap consults. An app_start used as a command substitution would register
# in a subshell and leave this empty.
app_start "$LOG/registered.log" sleep 300; first=$APP_LAST_GROUP
app_start "$LOG/registered2.log" sleep 300; second=$APP_LAST_GROUP
[ "${#APP_GROUPS[@]}" = 2 ] || fail "two launches registered ${#APP_GROUPS[@]} handles"
app_stop_all
for group in "$first" "$second"; do
  app_owned_alive "$group" && fail "app_stop_all left $group alive"
done
[ "${#APP_GROUPS[@]}" = 0 ] || fail "app_stop_all left ${#APP_GROUPS[@]} handles registered"

# 6. The predicates themselves. Every "it is gone" rests on them, so both
# negative sides are checked rather than assumed: a handle nothing is using, and
# a live process that is NOT an ownable launch because it is in the driver's own
# session — which is the transient state job control would otherwise offer.
unused=4194303
app_owned_alive "$unused" && fail "a handle nothing is using reported alive"
app_group_wait "$unused" 1 && fail "the ownership wait succeeded on an unused handle"
# Registered like any other launch, and silenced: a fixture that keeps the
# caller's stderr blocks whoever captures this script's output if a failing case
# ever leaks it, which is how the leak this case used to have was found.
sleep 300 >/dev/null 2>&1 & inherited=$!
APP_GROUPS+=("$inherited")
app_group_wait "$inherited" 1 && fail "the ownership wait accepted a process in the driver's own session"
app_stop "$inherited" 10 || fail "a process in the driver's own session was not stopped"

# 7. Job control is refused before anything is launched, and the refusal says
# why. Under it bash gives the background child its own group before setsid runs,
# so a check that accepted that transient group would own nothing a moment later.
(
  set -m
  app_start "$LOG/jobcontrol.log" sleep 3 && exit 1
  exit 0
) 2>"$LOG/jobcontrol.err" || fail "app_start accepted a launch under job control"
grep -q "job control" "$LOG/jobcontrol.err" \
  || fail "the refusal did not name job control: $(tr -d '\n' <"$LOG/jobcontrol.err")"

# 8. The pending state every launch passes through, constructed on purpose
# because the moment between the fork and setsid's call cannot be held open: a
# live child still in the DRIVER's group, registered under its pid. No group
# signal reaches it, and a review found one surviving a driver that thought it had
# stopped it.
sleep 300 >/dev/null 2>&1 & pending=$!
APP_GROUPS+=("$pending")
app_owned_alive "$pending" || fail "a pending child was not seen as alive"
app_stop "$pending" 10 || fail "a pending child in the driver's own group was not stopped"
app_leader_running "$pending" && fail "a pending child survived app_stop"

# 9. The same pending state through the trap: an interrupted driver must leave
# nothing even when its launch has not become its own session yet.
pending=$(bash -c '
  set -euo pipefail
  . scripts/app-process.sh
  app_install_traps
  sleep 300 >/dev/null 2>&1 & one=$!
  APP_GROUPS+=("$one")
  printf "%s\n" "$one"
  kill -TERM $$
  sleep 30
') || true
[ -n "$pending" ] || fail "the interrupted driver reported no pending launch"
for _ in $(seq 1 10); do
  app_leader_running "$pending" || break
  sleep 1
done
app_leader_running "$pending" && fail "a pending launch outlived the interrupted driver"

printf 'app-process: PASS — stop confirmed for the ordinary, TERM-resistant,'
printf ' child-bearing, interrupted and still-pending launches; job control'
printf ' refused; and neither an unused handle nor a process in this session'
printf ' reported ownable\n'
