#!/bin/bash
set -euo pipefail
: "${PERSONA_APPARMOR_PROFILES_FILE:?synthetic profile list required}"
action=""
for argument in "$@"; do case "$argument" in -a|-R) action="$argument" ;; esac; done
if [[ "${1:-}" == -Q ]]; then
  while [[ "$#" -gt 0 ]]; do
    if [[ "$1" == -o ]]; then printf 'synthetic-compiled-policy\n' > "$2"; exit 0; fi
    shift
  done
  exit 2
fi
if [[ "$action" == -a ]]; then
  printf 'persona-first-run-12345-1 (unconfined)\n' > "$PERSONA_APPARMOR_PROFILES_FILE"
  exit 1
fi
if [[ "$action" == -R ]]; then
  # If the proposal wrongly removes a colliding foreign profile, this file becomes empty.
  : > "$PERSONA_APPARMOR_PROFILES_FILE"
fi
