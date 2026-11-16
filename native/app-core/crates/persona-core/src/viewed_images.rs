//! The rendered viewed-image leaf of `ViewedImages.images(of: ToolCall)`, from the existing lines input. This is not
//! the whole `ChatLine` projection: folded images, UI loading and actual tool execution stay outside this slice.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, Deserialize)]
pub struct Input {
    pub items: Vec<Item>,
    pub line_id: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct Item {
    pub id: String,
    pub seq: i64,
    pub role: String,
    pub kind: String,
    pub tool_use_id: Option<String>,
    pub tool_name: Option<String>,
    pub tool_input: Option<Value>,
    pub is_error: Option<bool>,
    pub image: Option<ImagePayload>,
    pub task: Option<Task>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct Task {
    pub kind: String,
    pub status: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct ImagePayload {
    pub data: String,
    #[serde(default)]
    pub mime: String,
    #[serde(rename = "ref")]
    pub ref_: Option<String>,
    pub size: Option<i64>,
    pub width: Option<i64>,
    pub height: Option<i64>,
}

#[derive(Debug, Clone, Serialize)]
pub struct ViewedLine {
    pub id: String,
    pub image_ids: Vec<String>,
    pub is_input: bool,
    #[serde(rename = "type")]
    pub kind: String,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Source {
    Read,
    Screenshot,
    Browser,
    Mcp,
    Tool,
}

fn source(tool: &str, input: &Value) -> (Source, Option<String>) {
    let (base, mcp) = if let Some(rest) = tool.strip_prefix("mcp__") {
        match rest.rfind("__") {
            Some(index) if index > 0 => (&rest[index + 2..], true),
            _ => (tool, false),
        }
    } else {
        (tool, false)
    };
    let nonempty = |key: &str| {
        input
            .get(key)
            .and_then(Value::as_str)
            .filter(|text| !text.is_empty())
            .map(str::to_owned)
    };
    match base {
        "Read" if !mcp => (Source::Read, nonempty("file_path")),
        _ if mcp && base.starts_with("computer_") => (Source::Screenshot, nonempty("machine")),
        _ if mcp && base.starts_with("browser_") => (Source::Browser, nonempty("machine")),
        _ if mcp => (Source::Mcp, None),
        _ => (Source::Tool, None),
    }
}

fn picture(path: &str) -> bool {
    let extension = path
        .rsplit_once('.')
        .map(|(_, extension)| extension.to_ascii_lowercase());
    extension.is_some_and(|extension| {
        [
            "png", "jpg", "jpeg", "gif", "webp", "heic", "heif", "bmp", "tif", "tiff",
        ]
        .contains(&extension.as_str())
    })
}

fn key(path: Option<&str>, image: &ImagePayload) -> Option<String> {
    if let Some(path) = path {
        return Some(format!("p:{path}"));
    }
    let size = image
        .size
        .or_else(|| (!image.data.is_empty()).then_some(image.data.len() as i64 * 3 / 4));
    if let (Some(size), Some(width), Some(height)) = (size, image.width, image.height)
        && size > 0
        && width > 0
        && height > 0
    {
        return Some(format!("s:{}:{size}:{width}x{height}", image.mime));
    }
    let identity = image.ref_.as_deref().unwrap_or_else(|| {
        // ImagePayload.identity is only truncated for inline data, never for a supplied ref.
        &image.data
    });
    if image.ref_.is_some() {
        return Some(format!("d:{identity}"));
    }
    let prefix = identity.chars().take(64).collect::<String>();
    Some(format!("d:{prefix}{}", identity.chars().count()))
}

/// Projects a single tool call's viewed strip. `line_id` is supplied by the containing work item, not inferred from
/// the tool call. Only its complete image IDs are part of this leaf; rendering, path payloads and other lines are not.
pub fn project(input: &Input) -> Option<ViewedLine> {
    let line_id = input
        .line_id
        .as_deref()
        .or_else(|| {
            input
                .items
                .iter()
                .find(|item| {
                    item.role == "user" && matches!(item.kind.as_str(), "text" | "image") && item.tool_use_id.is_none()
                })
                .map(|item| item.id.as_str())
        })
        .or_else(|| input.items.first().map(|item| item.id.as_str()))
        .unwrap_or_default();
    let mut uses = BTreeMap::new();
    let mut results: BTreeMap<&str, Vec<&Item>> = BTreeMap::new();
    let mut images: BTreeMap<&str, Vec<&ImagePayload>> = BTreeMap::new();
    for item in &input.items {
        let Some(id) = item.tool_use_id.as_deref() else {
            continue;
        };
        match item.kind.as_str() {
            "tool_use" => {
                uses.insert(id, item);
            }
            "tool_result" => results.entry(id).or_default().push(item),
            "image" => {
                if let Some(image) = &item.image {
                    images.entry(id).or_default().push(image);
                }
            }
            _ => {}
        }
    }
    let (id, tool_use) = uses.iter().next()?;
    let turn_input: crate::turns::Input = serde_json::from_value(serde_json::json!({"items": input.items})).ok()?;
    let group_id = format!("tools-{}", tool_use.id);
    let turns = crate::turns::project(&turn_input);
    let owner = turns.as_array()?.iter().find(|turn| {
        turn["ordered_ids"]
            .as_array()
            .is_some_and(|ids| ids.iter().any(|item| item.as_str() == Some(group_id.as_str())))
    });
    let owner = owner?;
    let owner = input
        .line_id
        .as_deref()
        .or_else(|| owner.get("user_ids")?.as_array()?.first()?.as_str())
        .unwrap_or(line_id);
    let (_source, path) = source(
        tool_use.tool_name.as_deref().unwrap_or("tool"),
        &tool_use.tool_input.clone().unwrap_or(Value::Null),
    );
    let calls_images = images.get(id).into_iter().flatten().copied().collect::<Vec<_>>();
    let result = results.get(id).and_then(|items| items.last());
    let running = result.is_none()
        || result.and_then(|item| item.task.as_ref()).is_some_and(|task| {
            task.kind == "agent"
                && !matches!(
                    task.status.as_deref(),
                    Some("completed" | "failed" | "error" | "killed" | "stopped")
                )
        });
    let error = result.is_some_and(|item| {
        item.is_error == Some(true)
            || item
                .task
                .as_ref()
                .is_some_and(|task| matches!(task.status.as_deref(), Some("failed" | "error")))
    });
    let mut image_ids = Vec::new();
    if !calls_images.is_empty() {
        let file = (calls_images.len() == 1).then_some(path.as_deref()).flatten();
        image_ids.extend(calls_images.iter().filter_map(|image| key(file, image)));
    } else if !running && !error && path.as_deref().is_some_and(picture) {
        image_ids.extend(key(
            path.as_deref(),
            &ImagePayload {
                data: String::new(),
                mime: String::new(),
                ref_: None,
                size: None,
                width: None,
                height: None,
            },
        ));
    }
    let mut seen = BTreeSet::new();
    let image_ids = image_ids
        .into_iter()
        .filter(|id| seen.insert(id.to_string()))
        .collect::<Vec<_>>();
    if image_ids.is_empty() {
        return None;
    }
    Some(ViewedLine {
        id: format!("vi-{owner}"),
        image_ids,
        is_input: false,
        kind: "viewed".into(),
    })
}
