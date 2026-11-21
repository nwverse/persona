#!/bin/bash
# Read-only diagnostics on a disposable hosted runner. No policy, mount, permission or process changes.
set -uo pipefail
: "${GITHUB_RUN_ID:?run id required}" "${GITHUB_RUN_ATTEMPT:?run attempt required}" "${RUNNER_TEMP:?temporary directory required}"
[[ "$GITHUB_RUN_ID" =~ ^[0-9]+$ && "$GITHUB_RUN_ATTEMPT" =~ ^[0-9]+$ ]] || exit 2
[[ "${GITHUB_ACTIONS:-}" == true && "${CI:-}" == true ]] || exit 2
phase="${1:?usage: desktop-first-run-apparmor-diagnostics.sh before-load|after-load|after-cleanup}"
case "$phase" in before-load|after-load|after-cleanup) ;; *) exit 2 ;; esac
name="persona-first-run-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT"
destination="$RUNNER_TEMP/$name.diagnostics"
mkdir -p -- "$destination"
profiles=/sys/kernel/security/apparmor/profiles
status=0
capture() {
  local output="$1"
  shift
  local result
  local absent_ok=0
  if [[ "$output" == profiles-grep.txt ]]; then absent_ok=1; fi
  {
    printf 'command:'
    printf ' %q' "$@"
    printf '\n'
    "$@"
    result=$?
    printf 'exit=%s\n' "$result"
  } > "$destination/$output" 2>&1
  # grep 1 is no exact match (absence); 2+ is an unreadable/missing interface or another query error.
  if [[ "$result" -ne 0 && ! ( "$absent_ok" -eq 1 && "$result" -eq 1 ) ]]; then status=1; fi
}
# The approved sudo invocations are read-only: grep, ls, cat and aa-status.
capture profiles-grep.txt sudo grep -E -e "^${name} \\([^)]*\\)$" -- "$profiles"
capture profiles-ls.txt sudo ls -l -- "$profiles"
capture securityfs-ls.txt sudo ls -ld -- /sys/kernel/security /sys/kernel/security/apparmor
capture profiles-cat.txt sudo cat -- "$profiles"
capture aa-status.txt sudo aa-status --enabled
capture aa-status-profiles.txt sudo aa-status
capture mounts-grep.txt sudo grep -E -e 'apparmor|securityfs' -- /proc/mounts
printf 'AppArmor read-only diagnostics (%s): %s; evidence %s\n' "$phase" "$([ "$status" -eq 0 ] && printf PASS || printf 'DIAGNOSTIC COMMANDS FAILED')" "$destination"
exit "$status"
