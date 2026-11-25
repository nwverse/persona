accepted closes work after delivery (default delivery_gate=merged: park verified open PRs; linked issues must close). user_check keeps the task and Agent open until owner Pass and routes feedback back. If opted in at Start, accepted done cleans up an idle tab unless user_check. Report closed/skipped/pending; never close tabs yourself. Rework caps stalled/low-ROI churn at 2 rounds, external_input/substantive_new at 10 total. Accept low-ROI edge cases or style nits with a note. Replace stopped Agents via agent_start. Use parked with external evidence or needs_user for owner-only contact, approval or action.

For accepted (outcome=done), list every done_when once in checks (met=true, nonblank evidence), even with user_check. Exempt outcome=cannot, cancelled and no-criteria tasks. Other verdicts may omit checks and keep progress. Report met/not_met/dropped per criterion, then also_done. Judge the scoped brief over handovers or claims.

For deferred original scope, ask via needs_user or include deferred_scope with user_check or an open linked follow_up_todo_id, even when all checks pass.

Give accepted outcome/reason/link. For needs_user use a direct PR/MR/doc URL when available. The hub sends conclusion cards and passes verification unless user_check. At 24h/deadline needs_user defaults to recommend; irreversible/visible_to_others only remind. Recheck at 24h. “You decide”, “This isn't what I asked for” and “Tell me the background first” return to supervision. Clarify without repetition or irreversible/external action.

Non-main reviewers of others' tasks may only accept/cancel; send gaps via agent_send. Leaders' needs_user also reaches main. Cancel only duplicates/unwanted tasks; their Agents close/archive and cards end.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["todo_id", "verdict"],
  "properties": {
    "todo_id": {
      "type": "string",
      "description": "todo id (todo_…) from 督办 or context"
    },
    "verdict": {
      "type": "string",
      "enum": ["accepted", "rework", "needs_user", "parked", "cancelled"]
    },
    "rework_kind": {
      "type": "string",
      "enum": ["external_input", "substantive_new", "repeated_error", "low_roi_edge"],
      "description": "rework: external_input (review/conflict/owner input) skips the stalled cap; substantive_new (gap fixed, new defect) resets it; default repeated_error/low_roi_edge caps at 2 stalled, 10 total"
    },
    "checks": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["criterion", "met"],
        "properties": {
          "criterion": {"type": "string", "description": "one done_when criterion, or task purpose if none"},
          "met": {"type": "boolean"},
          "evidence": {"type": "string", "description": "proof (link/output) or what's missing"}
        }
      },
      "description": "updates criterion state, evidence, time and supervisor; omissions keep progress"
    },
    "note": {
      "type": "string",
      "description": "{{if eq .Lang "en"}}rework: what the agent is to do (English, concrete); needs_user: what the user is to decide; parked: anything more than reason, for your own record; cancelled: why; accepted: the result in a line{{else if eq .Lang `ja`}}rework は Agent が行う具体的な作業を日本語で記述します。needs_user はユーザーが決めること、parked は reason 以外に記録する補足、cancelled は理由、accepted は結果を一行で記述します{{else}}rework: what the agent is to do (Chinese, concrete); needs_user: what the user is to decide; parked: anything more than reason, for your own record; cancelled: why; accepted: the result in a line{{end}}"
    },
    "outcome": {
      "type": "string",
      "enum": ["done", "cannot"],
      "description": "accepted: done (complete or yes; default) or cannot (proved impossible)"
    },
    "reason": {
      "type": "string",
      "description": "one sentence in the user's language: accepted why (default: note's first line), needs_user decision (required), or parked wait (required; link PR if relevant)"
    },
    "waiting_on": {
      "type": "string",
      "enum": ["someone_else", "external_event", "date"],
      "description": "parked (required): person action/reply, external event, or date. Owner decisions use needs_user; legacy owner_priority stays readable"
    },
    "until": {
      "type": "string",
      "description": "parked, optional: duration (\"3d\", \"36h\", \"90m\") or RFC 3339 time, 1m–30d ahead; default 24h. waiting_on=date requires explicit RFC 3339"
    },
    "link": {
      "type": "string",
      "description": "accepted/needs_user card URL: direct PR/MR/doc, or defaults to task PR/MR, note/checks URL or chat"
    },
    "recommend": {
      "type": "string",
      "description": "needs_user (required): first option label; owner OK sends it to the Agent"
    },
    "why_owner": {
      "type": "string",
      "enum": ["irreversible", "visible_to_others", "priority", "acceptance", "only_owner_knows"],
      "description": "needs_user (required): irreversible (delete/prod/spend), visible_to_others (external message/comment/review), priority (whether/order), acceptance (visual check), only_owner_knows (private fact); otherwise decide yourself"
    },
    "options": {
      "type": "array",
      "minItems": 2,
      "maxItems": 4,
      "items": {
        "type": "object",
        "required": ["label", "leads_to"],
        "properties": {
          "label": {"type": "string", "description": "short button label"},
          "leads_to": {"type": "string", "description": "one sentence: what choosing it leads to"}
        }
      },
      "description": "needs_user (required): 2–4 options, recommended first"
    },
    "cost_of_waiting": {
      "type": "string",
      "description": "needs_user (required): one sentence on the cost of waiting"
    },
    "question_quote": {
      "type": "string",
      "description": "needs_user: the Agent's question verbatim when reason paraphrases it (hub checks for an existing reply)"
    },
    "can_wait": {
      "type": "boolean",
      "description": "needs_user: low-cost wait, nobody blocked today; list in Needs you without push/card. Requires reversible, excludes irreversible/visible_to_others, and needs a briefing with facts within 24h and >=12h before deadline; default false"
    },
    "deadline": {
      "type": "string",
      "description": "needs_user, optional: how long the decision waits, duration (\"4h\") or RFC 3339 time, 1m–30d ahead; needs reversible. Default 24h reversible; none for why_owner irreversible/visible_to_others"
    },
    "also_done": {
      "type": "array",
      "maxItems": 6,
      "items": {"type": "string"},
      "description": "accepted: work beyond brief, one line each, shown after criteria"
    },
    "withdraw_needs_user": {
      "type": "boolean",
      "description": "accepted on needs_user: supervisor or main withdraws and closes silently"
    },
    "user_check": {
      "type": "boolean",
      "description": "accepted only: user must inspect result (e.g. UI change); shows \"Please verify\" card awaiting \"Pass\" instead of auto-passing; default false"
    },
    "deferred_scope": {
      "type": "string",
      "maxLength": 600,
      "description": "accepted: original work deferred or needing approval, in the owner's language. Verify card shows first 120 characters"
    },
    "follow_up_todo_id": {
      "type": "string",
      "description": "With deferred_scope and no user_check: open follow-up whose part_of or after names this to-do"
    },
    "reversible": {
      "type": "boolean",
      "description": "with deadline: whether taking recommend can be undone; false for delete/external send/spend/prod change (never auto-taken; required false for irreversible/visible_to_others)"
    },
    "evidence": {
      "type": "object",
      "required": ["kind", "target", "wake", "next_actor"],
      "properties": {
        "kind": {
          "type": "string",
          "enum": ["person_reply", "merge_review", "external_event", "date"],
          "description": "External wait kind"
        },
        "target": {"type": "string", "maxLength": 200, "description": "The person or external object"},
        "wake": {"type": "string", "maxLength": 600, "description": "Observable change that ends the wait"},
        "next_actor": {"type": "string", "enum": ["external", "owner"], "description": "external: third party, system or time, without a prior owner step. owner: use needs_user"},
        "url": {"type": "string", "maxLength": 2000, "description": "Required for merge_review (ready PR/MR) and external_event"},
        "interaction_id": {"type": "string", "maxLength": 200, "description": "external_event only. This worker's exact pending owner request ID from agents_list. Shows the existing request"},
        "pr_event": {
          "type": "string",
          "enum": ["terminal", "terminal_or_review"],
          "description": "GitHub PR waits only. terminal for merge or close. terminal_or_review adds new submitted reviews"
        },
        "contact": {
          "type": "object",
          "required": ["channel", "url", "sender", "sent_at", "request", "status"],
          "properties": {
            "channel": {"type": "string", "maxLength": 100},
            "url": {"type": "string", "maxLength": 2000, "description": "sent-message/receipt link, not a channel homepage"},
            "sender": {"type": "string", "maxLength": 200},
            "sent_at": {"type": "string", "format": "date-time", "description": "actual sent time; not in the future"},
            "request": {"type": "string", "maxLength": 600},
            "status": {"type": "string", "enum": ["sent"]}
          },
          "description": "Sent request, required for person_reply and merge_review"
        },
        "ready": {"type": "boolean", "enum": [true], "description": "merge_review only. The PR/MR is ready for the reviewer"},
        "reply": {
          "type": "object",
          "required": ["source", "brand", "app_id", "account_open_id", "person_open_id", "chat_id", "thread_id", "request_message_id"],
          "properties": {
            "source": {"type": "string", "enum": ["larkcli"]},
            "brand": {"type": "string", "enum": ["feishu", "lark"]},
            "app_id": {"type": "string"},
            "account_open_id": {"type": "string"},
            "person_open_id": {"type": "string", "description": "expected human OpenID in this app, not self"},
            "chat_id": {"type": "string"},
            "thread_id": {"type": "string", "description": "provider topic ID, not root-message ID"},
            "request_message_id": {"type": "string"}
          },
          "description": "person_reply: exact IDs from a configured lark-cli reader, never names. contact.url must be the sent request's matching topic AppLink. A verified reply asks the current supervisor to review, not direct Agent delivery"
        }
      },
      "description": "Required for parked. Inspect the source. recorded_by is output only. Missing legacy evidence is unverified"
    }
  }
}
```
