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
            assert_eq!(
                persona_core::turns::project(&input),
                case["expected"]["turns"],
                "{}",
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
