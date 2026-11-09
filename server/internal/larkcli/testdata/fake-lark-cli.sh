#!/bin/sh
# A deterministic fake for the larkcli adapter tests. It never reads or writes
# a credential store and emits only synthetic fixture data.
set -eu

base=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scenario=${LARKCLI_FAKE_SCENARIO:-success}
phase=${LARKCLI_FAKE_PHASE:-initial}
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
      needs_refresh) cat "$base/auth_needs_refresh.json" ;;
      ready) cat "$base/auth_ready.json" ;;
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
    case "$scenario:${2:-}" in
      inbox:+messages-search)
        case " $* " in
          *" --sender "*) cat "$base/inbox_seed.json" ;;
          *) cat "$base/inbox_search.json" ;;
        esac
        ;;
      inbox_pages:+messages-search)
        case " $* " in
          *" --sender "*) cat "$base/inbox_seed.json" ;;
          *" --page-token "*) cat "$base/inbox_page2.json" ;;
          *) cat "$base/inbox_page1.json" ;;
        esac
        ;;
      inbox:+chat-messages-list|inbox:+threads-messages-list|inbox_pages:+chat-messages-list|inbox_pages:+threads-messages-list)
        cat "$base/inbox_context.json"
        ;;
      inbox:+messages-send|inbox:+messages-reply|inbox_pages:+messages-send|inbox_pages:+messages-reply)
        cat "$base/send_result.json"
        ;;
      *:+chat-list) cat "$base/chats.json" ;;
      *:+chat-messages-list|*:+threads-messages-list) cat "$base/messages.json" ;;
      *:+messages-search|*:+chat-search) cat "$base/search.json" ;;
      *) cat "$base/search.json" ;;
    esac
    exit 0
    ;;
  drive)
    case "${2:-}" in
      +inspect)
        cat "$base/document_inspect.json"
        exit 0
        ;;
      +list-comments)
        case "$scenario" in
          comments_unreadable)
            cat "$base/error_permission.json" >&2
            exit 3
            ;;
          comments_rate_limited)
            cat "$base/error_rate_limited.json" >&2
            exit 4
            ;;
          comments_reply)
            if [ "$phase" = "reply" ]; then
              cat "$base/comments_reply.json"
            else
              cat "$base/comments_initial.json"
            fi
            ;;
          comments|success)
            cat "$base/comments_initial.json"
            ;;
          *)
            cat "$base/replies_empty.json"
            ;;
        esac
        exit 0
        ;;
      +list-replies)
        cat "$base/replies_empty.json"
        exit 0
        ;;
      *)
        cat "$base/replies_empty.json"
        exit 0
        ;;
    esac
    ;;
  wiki)
    if [ "${2:-}" = "spaces" ] && [ "${3:-}" = "list" ]; then
      cat "$base/wiki_spaces.json"
      exit 0
    fi
    if [ "${2:-}" = "nodes" ] && [ "${3:-}" = "list" ]; then
      cat "$base/wiki_nodes.json"
      exit 0
    fi
    cat "$base/replies_empty.json"
    exit 0
    ;;
  *)
    if [ "$scenario" = "timeout" ]; then sleep 5; fi
    printf '%s\n' 'message-body-example' >&2
    exit 17
    ;;
esac
