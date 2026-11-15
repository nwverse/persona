//! Replays the static `lines` corpus's complete turn objects through the pure row/turn projection.

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

use persona_core::turns::Input;
use serde_json::Value;

#[test]
fn exact_static_turn_manifest_and_full_expected_objects() {
    let mut paths: Vec<PathBuf> =
        fs::read_dir(Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../../spec/fixtures/lines"))
            .unwrap()
            .map(|entry| entry.unwrap().path())
            .filter(|path| path.extension().is_some_and(|extension| extension == "json"))
            .collect();
    paths.sort();
    assert_eq!(paths.len(), 9, "the lines file set is the manifest boundary");
    let mut replayed = BTreeSet::new();
    let mut total = 0;
    for path in paths {
        let document: Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        for case in document["cases"].as_array().unwrap() {
            total += 1;
            if case["expected"].get("checkpoints").is_some() || case["input"].get("operations").is_some() {
                continue;
            }
            let name = case["name"].as_str().unwrap().to_owned();
            let input: Input = serde_json::from_value(case["input"].clone()).expect(&name);
            let mut actual = persona_core::turns::project(&input);
            for turn in actual.as_array_mut().unwrap() {
                turn.as_object_mut().unwrap().remove("has_pending_wake");
            }
            assert_eq!(
                actual,
                case["expected"]["turns"],
                "static turn corpus uses the legacy image/after-wake shape; #8239 owns the current Apple replay: {}",
                path.display()
            );
            assert!(replayed.insert(name.clone()), "duplicate case: {name}");
        }
    }
    assert_eq!(total, 61, "actual lines corpus size");
    let expected: BTreeSet<String> = [
        "absorbed_user_images",
        "background_running_badge",
        "closed_work_identity",
        "compaction_stays_inside_work",
        "empty_session",
        "empty_thinking_between_tools_is_retained",
        "failed_tool_counts_known_failure",
        "fold_counts_tools_and_thoughts",
        "frozen_snapshot_excludes_newer_items",
        "frozen_uses_numeric_block_order",
        "interrupt_is_trailing_notice",
        "last_result_for_call_wins",
        "late_result_attaches_to_original_call",
        "live_answer_after_wake",
        "live_empty_work",
        "live_queued_message_keeps_position",
        "live_reply_stays_after_work",
        "live_tail_runs_behind_chip",
        "long_answer_after_wake",
        "menu_owner_per_message",
        "missing_start_time_hides_duration",
        "missing_start_time_uses_first_step",
        "missing_tool_result_is_running",
        "monitor_event_uses_first_detail_line",
        "multiple_interactions_in_footer",
        "multiple_wakes_pick_latest_answer",
        "notification_without_wake",
        "older_page_stays_visible_during_freeze",
        "only_last_reply_in_each_wake_is_candidate",
        "open_queued_messages_work",
        "open_work_identity",
        "orphan_assistant",
        "orphan_tool_result_is_not_a_row",
        "orphan_wake_opens_with_notice",
        "ordinary_user_starts_next_turn",
        "photos_after_wake_stay_with_message",
        "plain_exchange",
        "queued_without_existing_turn",
        "recap_separates_turns",
        "regular_user_images",
        "short_reply_after_wake_is_the_answer",
        "single_interaction_in_footer",
        "standalone_empty_thinking_hidden",
        "task_notices_find_calls",
        "task_status_outcomes",
        "task_title_containing_interrupt_is_not_interrupt",
        "task_without_loaded_call_uses_title",
        "thinking_before_between_after_tools",
        "thinking_narration_preserved",
        "three_users_apart_without_steps",
        "timed_work_ends_at_answer",
        "tool_images_belong_to_call",
        "trailing_tool_after_answer",
        "unknown_item_is_ignored",
        "wake_after_recap_reopens_turn",
        "wake_without_any_reply",
        "finished_queued_messages_apart",
    ]
    .into_iter()
    .map(str::to_owned)
    .collect();
    assert_eq!(
        replayed, expected,
        "exact static turn set; four operation cases are excluded"
    );
    assert_eq!(replayed.len(), 57);
    assert!(replayed.contains("wake_after_recap_reopens_turn"));
    assert!(replayed.contains("photos_after_wake_stay_with_message"));
    assert!(replayed.contains("finished_queued_messages_apart"));
    eprintln!(
        "turn projection: {} exact static cases, full expected turns equality",
        replayed.len()
    );
}

#[test]
fn choosing_the_first_answer_fails_the_wake_corpus() {
    let path = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../../spec/fixtures/lines/wakes.json");
    let document: Value = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
    let mut detected = 0;
    for case in document["cases"].as_array().unwrap() {
        let input: Input = serde_json::from_value(case["input"].clone()).unwrap();
        let actual = persona_core::turns::answer_mutation(&input);
        let expected = case["expected"]["turns"][0]["answer_id"].clone();
        if actual.as_ref().map(|id| Value::String(id.clone())).as_ref() != Some(&expected) {
            detected += 1;
        }
    }
    assert!(
        detected > 0,
        "a first-answer mutation must not satisfy every wake fixture"
    );
}

/// Expectations below are written from current Apple source/tests, not from this Rust implementation's output.
/// They supplement rather than rewrite the shared lines fixtures while #8239 repairs the executable Apple replay.
mod apple_source_regressions {
    use persona_core::turns::Input;
    use serde_json::{Value, json};

    fn project(items: Value) -> Value {
        assert!(items.is_array(), "test helper expects an item array");
        let input: Input = serde_json::from_value(json!({"items": items})).unwrap();
        persona_core::turns::project(&input)
    }

    #[test]
    fn synthetic_supervision_json_shape() {
        let text = "📋 督办：你派出去的任务有 2 个要你处理\n这是 Persona 的督办消息";
        for marked in [false, true] {
            let items = json!([
                {"id":"u1#0","seq":1,"role":"user","kind":"text","text":"First real request"},
                {"id":"u2#0","seq":2,"role":"user","kind":"text","text":text,"queued":true,
                 "injected":if marked {json!({"state":"supervise"})} else {Value::Null}},
                {"id":"u3#0","seq":3,"role":"user","kind":"text","text":"Next real request","queued":true},
            ]);
            assert!(items.is_array(), "items: {items}");
            let turns = project(items);
            assert_eq!(turns.as_array().unwrap().len(), 3);
        }
    }

    #[test]
    fn same_entry_text_image_text_stays_in_users_and_a_later_real_input_is_separate() {
        let turns = project(json!([
            {"id":"u1#0","seq":1,"role":"user","kind":"text","text":"First real request"},
            {"id":"t2#0","seq":2,"role":"assistant","kind":"tool_use","tool_use_id":"call2"},
            {"id":"u3#0","seq":3,"role":"user","kind":"image"},
            {"id":"u3#1","seq":3,"role":"user","kind":"text","queued":true,
             "injected":{"state":"supervise"}},
            {"id":"u3#2","seq":3,"role":"user","kind":"text","text":"Extra fictional detail"},
            {"id":"t4#0","seq":4,"role":"assistant","kind":"tool_use","tool_use_id":"call4"},
            {"id":"u5#0","seq":5,"role":"user","kind":"text","text":"Next real request","queued":true},
        ]));
        assert_eq!(turns.as_array().unwrap().len(), 3);
        assert_eq!(turns[0]["id"], "u1#0");
        assert_eq!(turns[1]["id"], "u3#1");
        assert_eq!(turns[1]["user_ids"], json!(["u3#1", "u3#0", "u3#2"]));
        assert_eq!(turns[1]["process_ids"], json!(["tools-t4#0"]));
        assert_eq!(turns[2]["user_ids"], json!(["u5#0"]));
        assert_eq!(turns[1]["has_pending_wake"], false);

        let image_only = project(json!([
            {"id":"u1#0","seq":1,"role":"user","kind":"text","injected":{"state":"supervise"}},
            {"id":"u4#0","seq":4,"role":"user","kind":"image"},
            {"id":"a5#0","seq":5,"role":"assistant","kind":"text"},
        ]));
        assert_eq!(image_only.as_array().unwrap().len(), 2);
        assert_eq!(image_only[1]["user_ids"], json!(["u4#0"]));
    }

    #[test]
    fn frozen_cut_precedes_tool_pairing_and_ordered_timestamps() {
        let mut input: Input = serde_json::from_value(json!({
            "frozen":{"seq":2,"part":0},
            "items":[
                {"id":"u1#0","seq":1,"role":"user","kind":"text","ts":"2026-01-01T00:00:10Z"},
                {"id":"t2#0","seq":2,"role":"assistant","kind":"tool_use","tool_use_id":"call2","ts":"2026-01-01T00:00:20Z"},
                {"id":"r3#0","seq":3,"role":"user","kind":"tool_result","tool_use_id":"call2","ts":"2026-01-01T00:00:05Z"},
                {"id":"t4#0","seq":4,"role":"assistant","kind":"tool_use","tool_use_id":"call4","ts":"2026-01-01T00:00:07Z"},
                {"id":"a5#0","seq":5,"role":"assistant","kind":"text","ts":"2026-01-01T00:00:30Z"},
            ]
        })).unwrap();
        let turns = persona_core::turns::project(&input);
        assert_eq!(turns[0]["ordered_ids"], json!(["u1#0", "tools-t2#0"]));
        assert_eq!(turns[0]["process_ids"], json!(["tools-t2#0"]));
        assert_eq!(turns[0]["end"], "2026-01-01T00:00:20Z");

        input.frozen = None;
        let turns = persona_core::turns::project(&input);
        assert_eq!(turns[0]["ordered_ids"], json!(["u1#0", "tools-t2#0", "a5#0"]));
        assert_eq!(turns[0]["process_ids"], json!(["tools-t2#0"]));
        assert_eq!(turns[0]["start"], "2026-01-01T00:00:10Z");
        assert_eq!(turns[0]["end"], "2026-01-01T00:00:30Z");
    }

    #[test]
    fn tool_result_wake_reopens_the_turn_without_becoming_a_user_message() {
        let items = json!([
            {"id":"q1#0","seq":1,"role":"user","kind":"text"},
            {"id":"a2#0","seq":2,"role":"assistant","kind":"text"},
            {"id":"rc3#0","seq":3,"role":"system","kind":"recap"},
            {"id":"r4#0","seq":4,"role":"user","kind":"tool_result","tool_use_id":"agentcall",
             "task":{"kind":"agent"},"wake":true},
            {"id":"a5#0","seq":5,"role":"assistant","kind":"text"},
        ]);
        let turns = project(json!(items));
        assert_eq!(turns.as_array().unwrap().len(), 1, "wake: {turns}");
        assert_eq!(turns[0]["user_ids"], json!(["q1#0"]));
        assert_eq!(turns[0]["wake_ids"], json!(["r4#0"]), "{turns}");
        assert_eq!(turns[0]["process_ids"], json!(["a2#0", "r4#0"]));
        assert_eq!(turns[0]["answer_id"], "a5#0", "wake: {turns}");
        assert_eq!(turns[0]["has_pending_wake"], false);

        let mut not_a_wake = items;
        not_a_wake[3]["wake"] = Value::Bool(false);
        let turns = project(not_a_wake);
        assert_eq!(turns.as_array().unwrap().len(), 2, "not-a-wake: {turns}");
        assert_eq!(turns[0]["wake_ids"], json!([]), "not-a-wake: {turns}");
        assert_eq!(turns[0]["answer_id"], "a2#0", "not-a-wake: {turns}");
        assert_eq!(turns[1]["answer_id"], "a5#0", "not-a-wake: {turns}");
        let pending = project(json!([
            {"id":"q1#0","seq":1,"role":"user","kind":"text"},
            {"id":"a2#0","seq":2,"role":"assistant","kind":"text"},
            {"id":"w3#0","seq":3,"role":"system","kind":"notice","wake":true},
            {"id":"t4#0","seq":4,"role":"assistant","kind":"tool_use","tool_use_id":"call4"},
        ]));
        assert_eq!(pending[0]["answer_id"], "a2#0");
        assert_eq!(pending[0]["tail_ids"], json!([]));
        assert_eq!(pending[0]["has_pending_wake"], true);
    }

    #[test]
    fn compaction_notices_are_one_source_derived_step() {
        let input: Input = serde_json::from_value(json!({"items":[
            {"id":"u1#0","seq":1,"role":"user","kind":"text"},
            {"id":"a2#0","seq":2,"role":"assistant","kind":"text"},
            {"id":"n3#0","seq":3,"role":"system","kind":"notice","text":"上下文已压缩"}, // i18n-ignore
            {"id":"n4#0","seq":4,"role":"system","kind":"notice","text":"已从压缩的上下文继续"}, // i18n-ignore
            {"id":"n5#0","seq":5,"role":"system","kind":"notice","text":"Compacted while idle, fictional example"},
        ]}))
        .unwrap();
        let turns = persona_core::turns::project(&input);
        assert_eq!(turns[0]["ordered_ids"], json!(["u1#0", "a2#0", "n3#0"]));
        assert_eq!(turns[0]["process_ids"], json!(["n3#0"]));
        assert_eq!(turns[0]["answer_id"], "a2#0");

        let input: Input = serde_json::from_value(json!({"items":[
            {"id":"n1#0","seq":1,"role":"system","kind":"notice","text":"已从压缩的上下文继续"}, // i18n-ignore
            {"id":"u2#0","seq":2,"role":"user","kind":"text"},
        ]}))
        .unwrap();
        let turns = persona_core::turns::project(&input);
        assert_eq!(turns.as_array().unwrap().len(), 2);
        assert_eq!(turns[0]["ordered_ids"], json!(["n1#0"]));
        assert_eq!(turns[0]["process_ids"], json!(["n1#0"]));
        assert_eq!(turns[0]["answer_id"], Value::Null);
    }

    #[test]
    fn supervision_legacy_recognition_preserves_real_user_provenance() {
        // i18n-ignore: canonical injected protocol text, not localized UI copy.
        let text = "📋 督办：你派出去的任务有 2 个要你处理\n这是 Persona 的督办消息";
        for marked in [false, true] {
            let turns = project(json!([
                {"id":"u1#0","seq":1,"role":"user","kind":"text","text":"First real request"},
                {"id":"u2#0","seq":2,"role":"user","kind":"text","text":text,"queued":true,
                 "injected":if marked {json!({"state":"supervise"})} else {Value::Null}},
                {"id":"u3#0","seq":3,"role":"user","kind":"text","text":"Next real request","queued":true},
            ]));
            assert_eq!(turns.as_array().unwrap().len(), 3);
            assert_eq!(turns[1]["user_ids"], json!(["u2#0"]));
            assert_eq!(turns[2]["user_ids"], json!(["u3#0"]));
        }

        for (provenance, marker) in [
            ("client_msg_id", json!("fictional-message")),
            ("from", json!("fictional-peer")),
        ] {
            let mut value = json!({"items":[
                {"id":"u1#0","seq":1,"role":"user","kind":"text","text":text,"queued":true},
                {"id":"u2#0","seq":2,"role":"user","kind":"text","text":"Next real request","queued":true},
            ]});
            value["items"][0]
                .as_object_mut()
                .unwrap()
                .insert(provenance.to_owned(), marker);
            let turns = project(value["items"].take());
            assert_eq!(turns[0]["user_ids"], json!(["u1#0", "u2#0"]));
        }

        let protected = project(json!([
            {"id":"u0#0","seq":0,"role":"user","kind":"text","text":"First real request"},
            {"id":"u1#0","seq":1,"role":"user","kind":"text",
            "text":format!("<persona-context kind=\"message-source\">{{\"author\":\"user\"}}</persona-context>\n{text}"),
            "queued":true,
            "injected":{"state":"supervise"}},
            {"id":"u2#0","seq":2,"role":"user","kind":"text","text":"Next real request","queued":true},
        ]));
        assert_eq!(protected[0]["user_ids"], json!(["u0#0", "u1#0", "u2#0"]));
    }
}
