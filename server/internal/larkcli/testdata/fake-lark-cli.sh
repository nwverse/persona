#!/bin/sh
# A deterministic fake for the larkcli adapter tests. It never reads or writes
# a credential store and emits only synthetic fixture data.
set -eu

base=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scenario=${LARKCLI_FAKE_SCENARIO:-success}
if [ -n "${LARKCLI_FAKE_LOG:-}" ]; then
  printf '%s\n' "$*" >> "$LARKCLI_FAKE_LOG"
fi

if [ "${1:-}" = "--profile" ]; then
  shift 2
fi

case "${1:-}" in
  --version)
    if [ "$scenario" = "old" ]; then
      printf '%s\n' 'lark-cli version 1.0.71'
    else
      printf '%s\n' 'lark-cli version 1.0.99'
    fi
    exit 0
    ;;
  auth)
    case "$scenario" in
      not_logged_in) cat "$base/auth_not_logged_in.json" ;;
      expired) cat "$base/auth_expired.json" ;;
	  auth_missing_scope) cat "$base/auth_missing_scope.json" ;;
      expiring) cat "$base/auth_expiring.json" ;;
      *) cat "$base/auth_status.json" ;;
    esac
    exit 0
    ;;
  im)
    case "$scenario" in
      timeout) sleep 5; exit 0 ;;
      missing_scope) cat "$base/error_missing_scope.json"; exit 3 ;;
	  rate_limited) cat "$base/error_rate_limited.json"; exit 4 ;;
      nonzero) printf '%s\n' 'message-body-example' >&2; exit 17 ;;
    esac
    case "${2:-}" in
      +chat-list) cat "$base/chats.json" ;;
      +chat-messages-list|+threads-messages-list) cat "$base/messages.json" ;;
      +messages-search|+chat-search) cat "$base/search.json" ;;
      *) cat "$base/search.json" ;;
    esac
    exit 0
    ;;
  *)
    if [ "$scenario" = "timeout" ]; then sleep 5; fi
    printf '%s\n' 'message-body-example' >&2
    exit 17
    ;;
esac
