#!/bin/bash
set -euo pipefail
: "${PERSONA_APPARMOR_PROFILES_FILE:?synthetic profile list required}"
action=""
for argument in "$@"; do
  case "$argument" in -a|-R) action="$argument" ;; esac
done
if [[ "${1:-}" == -Q ]]; then
  while [[ "$#" -gt 0 ]]; do
    if [[ "$1" == -o ]]; then printf 'synthetic-compiled-policy\n' > "$2"; exit 0; fi
    shift
  done
  exit 2
fi
case "${PERSONA_APPARMOR_TEST_FAILURE:-}" in
  load-fails) [[ "$action" == -a ]] && exit 1 ;;
  remove-fails) [[ "$action" == -R ]] && exit 1 ;;
  query-fails) [[ "$action" == -R ]] && exit 1 ;;
  query-empty) [[ "$action" == -a ]] && exit 0 ;;
  query-after-load-fails)
    if [[ "$action" == -a ]]; then
      printf 'persona-first-run-12345-1 (unconfined)\n' >> "$PERSONA_APPARMOR_PROFILES_FILE"
      mv -- "$PERSONA_APPARMOR_PROFILES_FILE" "$PERSONA_APPARMOR_PROFILES_FILE.contents"
      mkdir -- "$PERSONA_APPARMOR_PROFILES_FILE"
      exit 0
    fi
    ;;
esac
case "$action" in
  -a) printf 'persona-first-run-12345-1 (unconfined)\n' >> "$PERSONA_APPARMOR_PROFILES_FILE" ;;
  -R) grep -E -v -e '^persona-first-run-12345-1 \([^)]*\)$' -- "$PERSONA_APPARMOR_PROFILES_FILE" > "$PERSONA_APPARMOR_PROFILES_FILE.tmp" || [[ "$?" == 1 ]]
      mv -f -- "$PERSONA_APPARMOR_PROFILES_FILE.tmp" "$PERSONA_APPARMOR_PROFILES_FILE" ;;
  *) exit 2 ;;
esac
