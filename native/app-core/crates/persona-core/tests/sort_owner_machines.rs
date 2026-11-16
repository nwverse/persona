use persona_core::sort::Input;
use serde_json::json;

fn input(grouping: &str, filter: &str) -> serde_json::Value {
    json!({
        "sessions": [
            {"machine_id": "shared", "session_id": "first", "app_hub_group": "hub", "manager": "alpha",
             "title": "First", "cwd": "/home/user/project", "state": "working"},
            {"machine_id": "shared", "session_id": "second", "app_hub_group": "hub", "manager": "beta",
             "title": "Second", "cwd": "/home/user/project", "state": "working"}
        ],
        "machines": [
            {"id": "shared", "name": "Shared", "group": "hub", "manager": "alpha"},
            {"id": "shared", "name": "Shared", "group": "hub", "manager": "beta"}
        ],
        "pins": {"spaces": [], "sessions": []},
        "mutes": {"spaces": [], "sessions": [], "allowed": []},
        "settings": {"grouping": grouping, "quick": "all", "query": "", "machine_filter": filter,
            "kept": [], "folded": [], "expanded_idle": [], "expanded_quiet": [], "compact": false}
    })
}

#[test]
fn managers_on_one_hub_machine_keep_independent_groups_and_filters() {
    let projection = persona_core::sort::project(&serde_json::from_value(input("machine", "")).expect("input"));
    let groups = projection.groups;
    assert_eq!(groups.len(), 2);
    assert_eq!(groups[0].id, "machine/hub|alpha|shared");
    assert_eq!(groups[0].rows, ["hub|shared/first"]);
    assert_eq!(groups[1].id, "machine/hub|beta|shared");
    assert_eq!(groups[1].rows, ["hub|shared/second"]);

    for (manager, session) in [("alpha", "first"), ("beta", "second")] {
        let filtered = persona_core::sort::project(&serde_json::from_value(input("machine", &format!("hub|{manager}|shared"))).expect("filter"));
        assert_eq!(filtered.groups.len(), 1);
        assert_eq!(filtered.groups[0].rows, [format!("hub|shared/{session}")]);
    }
}

#[test]
fn owner_qualified_space_group_keeps_legacy_preference_machine_key() {
    let mut value = input("space", "");
    value["pins"]["spaces"] = json!(["shared/project"]);
    value["sessions"][0]["space_name"] = json!("project");
    value["sessions"][1]["space_name"] = json!("project");
    let input: Input = serde_json::from_value(value).expect("space input");
    let projection = persona_core::sort::project(&input);
    assert_eq!(projection.groups.len(), 2);
    assert_eq!(projection.groups[0].id, "hub|alpha|shared/-");
    assert_eq!(projection.groups[1].id, "hub|beta|shared/-");
}
