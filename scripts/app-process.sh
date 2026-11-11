#!/usr/bin/env bash
# Process-group handling shared by the smoke driver and its self-check.
#
# A launched app is owned as a GROUP, never by a command line: a wrapper such as
# dbus-run-session does not pass a signal to its child, the webview starts
# children of its own, and the same binary can be invoked by different paths or
# by another run. The handle for all of that is the pid bash reports for the
# background setsid, which becomes the id of the new process group and session.
#
# That handle names two things, and BOTH are signalled and both are waited for:
# the group, which covers the app and everything it starts, and the leader pid,
# which exists from the moment of the fork. Between the fork and setsid's call
# the launch is still a child in the DRIVER's group, where no group signal
# reaches it; a review found such a child surviving an interrupted driver and then
# making its own session.
#
# Sourcing this file defines the functions and nothing else; it starts nothing.

# APP_GROUPS holds every handle started and not yet confirmed stopped, so an
# interrupted driver can stop them from its trap rather than leaving windows and
# state locks behind.
APP_GROUPS=()
# APP_LAST_GROUP is the handle app_start most recently started.
APP_LAST_GROUP=""

# app_group_alive <handle> — true while any process remains in the group.
app_group_alive() {
  pgrep -g "$1" >/dev/null 2>&1
}

# app_leader_running <handle> — true while the leader pid is a running process.
# A zombie is not running: the driver is its parent and has not reaped it yet.
app_leader_running() {
  local state
  state=$(ps -o stat= -p "$1" 2>/dev/null) || return 1
  state=${state// /}
  case "$state" in "" | Z*) return 1 ;; esac
  return 0
}

# app_owned_alive <handle> — anything this handle owns, in either state.
app_owned_alive() {
  app_group_alive "$1" && return 0
  app_leader_running "$1"
}

# app_survivors <handle> — what the handle still owns, for a message. Always
# succeeds: it is diagnostics, never a decision.
app_survivors() {
  { pgrep -g "$1" -a 2>/dev/null; ps -o pid=,stat=,args= -p "$1" 2>/dev/null; } \
    | tr -s ' ' | tr '\n' ';'
  return 0
}

# app_signal <signal> <handle> — to the group, and to the leader as well,
# because a launch interrupted before setsid ran has no group of its own.
app_signal() {
  kill -"$1" -- "-$2" >/dev/null 2>&1 || true
  kill -"$1" "$2" >/dev/null 2>&1 || true
}

# app_group_wait <handle> <seconds> — true once the handle leads a session of
# its own, which is what setsid produces and what makes the group signallable.
# A process in the driver's own session is not that, however alive it is: with
# job control bash gives a background child its own GROUP before setsid runs,
# and owning that transient state would own nothing a moment later.
app_group_wait() {
  local waited=0 session
  while :; do
    session=$(ps -o sid= -p "$1" 2>/dev/null | tr -d ' ')
    [ -n "$session" ] && [ "$session" = "$1" ] && return 0
    [ "$waited" -ge "$2" ] && return 1
    sleep 1
    waited=$((waited + 1))
  done
}

# app_forget <handle> — drop a handle from the registry, once it is gone.
app_forget() {
  local keep=() known
  for known in ${APP_GROUPS+"${APP_GROUPS[@]}"}; do
    [ "$known" = "$1" ] || keep+=("$known")
  done
  APP_GROUPS=(${keep+"${keep[@]}"})
}

# app_start <log> <command...> — run the command in its own process group and
# session with stdout and stderr in log, register the handle, confirm it is one
# this library can own, and report it in APP_LAST_GROUP. It is for an app that
# keeps running; a command that exits at once has nothing to own and is refused.
#
# It reports through a variable rather than stdout BECAUSE a command
# substitution is a subshell: an echoing version registered the handle inside
# that subshell, where the list died with it, so the caller's trap knew nothing
# about the process it was meant to stop. That is the same defect in different
# clothes as keeping the pid in a local, which is what a review found. Call it as
# a statement, never as $(app_start ...).
app_start() {
  # Job control is refused rather than probed: with it on, bash makes the
  # background child a group leader, so setsid must fork, and the pid bash
  # reports is a transient group in the driver's session that is gone a moment
  # later. Scripts have job control off; this says so instead of relying on it.
  case "$-" in
    *m*)
      printf 'app-process: job control is on, so a launch cannot be owned here\n' >&2
      return 1
      ;;
  esac
  local log="$1"; shift
  setsid "$@" >"$log" 2>&1 &
  # Registered before anything fallible: an interrupted driver must be able to
  # stop this launch even while it is still a child in the driver's own group.
  APP_LAST_GROUP=$!
  APP_GROUPS+=("$APP_LAST_GROUP")
  if ! app_group_wait "$APP_LAST_GROUP" 10; then
    printf 'app-process: no ownable session for: %s\n' "$*" >&2
    app_stop "$APP_LAST_GROUP" 5 || true
    return 1
  fi
}

# app_stop <handle> [seconds] — terminate everything the handle owns and WAIT
# for it to be gone, escalating once. Returns non-zero if anything survives,
# because a stop that cannot be confirmed is not a stop: the next case would
# inherit its window, its state lock and its log.
app_stop() {
  local handle="$1" patience="${2:-20}" waited=0
  app_signal TERM "$handle"
  while app_owned_alive "$handle"; do
    [ "$waited" -ge "$patience" ] && break
    sleep 1
    waited=$((waited + 1))
  done
  if app_owned_alive "$handle"; then
    # Said out loud, with what is still there: an escalation means something
    # ignored TERM, and a reader of a smoke artifact should see which process
    # that was rather than infer it. A fixture written as bash -c may appear
    # under the name of its last command, because bash execs that command in
    # place, and an ignored signal survives exec — which is exactly how the
    # self-check's TERM-resistant case resists.
    printf 'app-process: escalating to KILL for %s; still there: %s\n' \
      "$handle" "$(app_survivors "$handle")" >&2
    app_signal KILL "$handle"
    waited=0
    while app_owned_alive "$handle"; do
      [ "$waited" -ge 5 ] && return 1
      sleep 1
      waited=$((waited + 1))
    done
  fi
  app_forget "$handle"
  return 0
}

# app_install_traps [extra cleanup command] — stop everything this shell owns
# when it ends, however it ends.
#
# An EXIT trap is enough, which is MEASURED rather than assumed: bash runs an
# EXIT trap even when the shell is killed by TERM, INT or HUP, from outside or
# from itself, and whether or not it is waiting on a foreground child; and the
# trap body does not change the status the shell was going to exit with, so a
# failure stays a failure. An explicit trap on those signals would add no cover
# and would delay it, because bash defers a trapped signal until the foreground
# command in progress returns. Only KILL escapes this, and nothing covers that.
# An earlier version of this comment blamed the EXIT trap for a failure that was
# really the subshell registration above; the mutant run caught the wrong
# explanation, and the self-check's interrupted-driver cases are what prove the
# cleanup happens.
app_install_traps() {
  local extra="${1:-:}"
  # shellcheck disable=SC2064  # expand the caller's command now, on purpose
  trap "app_stop_all; $extra" EXIT
}

# app_stop_all — everything still registered, for an EXIT trap. Never fails the
# caller: it runs while the shell is already on its way out.
app_stop_all() {
  local handle
  for handle in ${APP_GROUPS+"${APP_GROUPS[@]}"}; do
    app_stop "$handle" 5 || true
  done
  APP_GROUPS=()
}
