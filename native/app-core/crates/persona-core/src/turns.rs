//! Transcript rows grouped into exchanges, following iOS's `ChatStore.buildRows` and `ChatBlock.build`.
//!
//! This is the turn-projection slice of `lines`, not the rendered `ChatLine` projection. It owns tool/result pairing,
//! user-picture placement, ordinary/queued turn boundaries, recap reopening and wake-aware answer selection. Times
//! are caller-supplied item timestamps; no clock or IO is read.

use serde::Deserialize;
use serde_json::Value;

use crate::state::utc_timestamp;

#[derive(Debug, Clone, Deserialize)]
pub struct Input {
    pub frozen: Option<Frozen>,
    /// Render options belong to the later ChatLine projection, not to turn grouping.
    #[serde(flatten)]
    pub options: serde_json::Map<String, Value>,
    pub items: Vec<Item>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Frozen {
    pub seq: i64,
    pub part: i64,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Item {
    pub id: String,
    pub seq: i64,
    pub ts: Option<String>,
    pub role: String,
    pub kind: String,
    pub text: Option<String>,
    pub tool_use_id: Option<String>,
    #[serde(default)]
    pub queued: Option<bool>,
    #[serde(default)]
    pub wake: Option<bool>,
    pub task: Option<Task>,
    pub injected: Option<Injected>,
    pub model_refusal: Option<Value>,
    pub fold: Option<Fold>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Task {
    pub kind: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Injected {
    pub state: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Fold {
    pub tools: i64,
    pub thoughts: i64,
    pub through_seq: i64,
    pub through_id: String,
    pub until: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Kind {
    User,
    Assistant,
    Thinking,
    Tool,
    Notice,
    Image,
    Recap,
}

#[derive(Debug, Clone)]
struct Row {
    id: String,
    kind: Kind,
    entry: Option<String>,
    queued: bool,
    supervision: bool,
    wake: bool,
    refusal: bool,
    interrupt: bool,
    start: Option<i128>,
    end: Option<i128>,
    tools: i64,
    thoughts: i64,
}

impl Row {
    fn user_input(&self) -> bool {
        self.kind == Kind::User || (self.kind == Kind::Image && self.entry.is_some())
    }

    fn assistant(&self) -> bool {
        self.kind == Kind::Assistant
    }

    fn first_ts(&self) -> Option<i128> {
        self.start
    }

    fn last_ts(&self) -> Option<i128> {
        self.end
    }
}

fn row(item: &Item) -> Option<Row> {
    let timestamp = || item.ts.as_deref().and_then(utc_timestamp);
    let kind = match item.kind.as_str() {
        "text" if item.role == "user" => Kind::User,
        "text" => Kind::Assistant,
        "thinking" => Kind::Thinking,
        "notice" => Kind::Notice,
        "image" => Kind::Image,
        "recap" => Kind::Recap,
        _ => return None,
    };
    Some(Row {
        id: item.id.clone(),
        kind,
        entry: ((kind == Kind::User) || (kind == Kind::Image && item.role == "user" && item.tool_use_id.is_none()))
            .then(|| item.id.split('#').next().unwrap_or(&item.id).to_owned()),
        queued: item.queued == Some(true) || kind == Kind::Image,
        supervision: item.injected.as_ref().and_then(|injected| injected.state.as_deref()) == Some("supervise"),
        wake: item.wake == Some(true),
        refusal: item.model_refusal.is_some(),
        interrupt: item.task.is_none() && item.text.as_deref().is_some_and(|text| text.contains("中断")), // i18n-ignore: hub notice
        start: timestamp(),
        end: timestamp(),
        tools: 0,
        thoughts: 0,
    })
}

fn rows(items: &[Item]) -> Vec<Row> {
    let mut ordered = Vec::with_capacity(items.len());
    let mut pictures = Vec::new();
    for item in items {
        let picture = item.kind == "image" && item.role == "user" && item.tool_use_id.is_none();
        if picture {
            if pictures.first().is_some_and(|first: &Item| first.seq != item.seq) {
                ordered.append(&mut pictures);
            }
            pictures.push(item.clone());
            continue;
        }
        if let Some(first) = pictures.first() {
            if item.kind == "text" && item.role == "user" && item.seq == first.seq {
                ordered.push(item.clone());
                ordered.append(&mut pictures);
            } else {
                ordered.append(&mut pictures);
                ordered.push(item.clone());
            }
        } else {
            ordered.push(item.clone());
        }
    }
    ordered.extend(pictures);

    let mut rows = Vec::new();
    let mut tools: Vec<Row> = Vec::new();
    let mut thinking: Vec<Row> = Vec::new();
    let flush_thoughts = |rows: &mut Vec<Row>, tools: &mut Vec<Row>, thinking: &mut Vec<Row>| {
        if tools.is_empty() {
            rows.append(thinking);
        } else {
            tools.append(thinking);
        }
    };
    let flush_run = |rows: &mut Vec<Row>, tools: &mut Vec<Row>| {
        if tools.is_empty() {
            return;
        }
        let id = format!("tools-{}", tools[0].id);
        rows.push(Row {
            id,
            kind: Kind::Tool,
            entry: None,
            queued: false,
            supervision: false,
            wake: false,
            refusal: false,
            interrupt: false,
            start: tools.iter().filter_map(Row::first_ts).min(),
            end: tools.iter().filter_map(Row::last_ts).max(),
            tools: tools.iter().map(|row| row.tools).sum(),
            thoughts: tools.iter().map(|row| row.thoughts).sum::<i64>()
                + tools.iter().filter(|row| row.kind == Kind::Thinking).count() as i64,
        });
        tools.clear();
    };
    for item in &ordered {
        match item.kind.as_str() {
            "tool_use" => {
                flush_thoughts(&mut rows, &mut tools, &mut thinking);
                let timestamp = item.ts.as_deref().and_then(utc_timestamp);
                let result_end = ordered
                    .iter()
                    .filter(|other| {
                        other.kind == "tool_result"
                            && other.tool_use_id.is_some()
                            && other.tool_use_id == item.tool_use_id
                    })
                    .filter_map(|other| other.ts.as_deref().and_then(utc_timestamp))
                    .next_back();
                tools.push(Row {
                    id: item.id.clone(),
                    kind: Kind::Tool,
                    entry: None,
                    queued: false,
                    supervision: false,
                    wake: false,
                    refusal: false,
                    interrupt: false,
                    start: timestamp,
                    end: result_end.or(timestamp),
                    tools: 1,
                    thoughts: 0,
                });
            }
            "thinking" => {
                if let Some(row) = row(item) {
                    thinking.push(row);
                }
            }
            "fold" => {
                flush_thoughts(&mut rows, &mut tools, &mut thinking);
                let timestamp = item.ts.as_deref().and_then(utc_timestamp);
                tools.push(Row {
                    id: item.id.clone(),
                    kind: Kind::Tool,
                    entry: None,
                    queued: false,
                    supervision: false,
                    wake: false,
                    refusal: false,
                    interrupt: false,
                    start: timestamp,
                    end: item
                        .fold
                        .as_ref()
                        .and_then(|fold| fold.until.as_deref())
                        .and_then(utc_timestamp)
                        .or(timestamp),
                    tools: item.fold.as_ref().map_or(0, |fold| fold.tools),
                    thoughts: item.fold.as_ref().map_or(0, |fold| fold.thoughts),
                });
            }
            "tool_result" => {
                if item.task.as_ref().is_some_and(|task| task.kind == "agent") && item.wake == Some(true) {
                    flush_thoughts(&mut rows, &mut tools, &mut thinking);
                    flush_run(&mut rows, &mut tools);
                    rows.extend(row(item));
                }
            }
            "image" if item.tool_use_id.is_some() => {}
            _ => {
                flush_thoughts(&mut rows, &mut tools, &mut thinking);
                flush_run(&mut rows, &mut tools);
                rows.extend(row(item));
            }
        }
    }
    flush_thoughts(&mut rows, &mut tools, &mut thinking);
    flush_run(&mut rows, &mut tools);
    rows
}

#[derive(Debug, Clone, Default)]
struct Turn {
    id: String,
    users: Vec<Row>,
    process: Vec<Row>,
    answer: Option<Row>,
    trailing: Vec<Row>,
    wakes: Vec<Row>,
    has_pending_wake: bool,
    tail: Vec<Row>,
    steps: Vec<Row>,
    ordered: Vec<Row>,
}

impl Turn {
    fn close(mut self) -> Self {
        if !self.wakes.is_empty() {
            let before = self.clone();
            let all = self.ordered.clone();
            let mut pick = None;
            self.has_pending_wake = false;
            for (index, row) in all.iter().enumerate() {
                if row.wake {
                    pick = None;
                    self.has_pending_wake = true;
                }
                if row.assistant() {
                    pick = Some(index);
                    self.has_pending_wake = false;
                }
            }
            if let Some(pick) = pick {
                self.tail = all[pick + 1..]
                    .iter()
                    .filter(|row| !row.user_input())
                    .cloned()
                    .collect();
                self.ordered = all[..=pick]
                    .iter()
                    .chain(all[pick + 1..].iter().filter(|row| row.user_input()))
                    .cloned()
                    .collect();
                self.process = self
                    .ordered
                    .iter()
                    .filter(|row| row.kind != Kind::User)
                    .cloned()
                    .collect();
            } else if before.process.iter().any(Row::assistant) {
                let wake = all.iter().rposition(|row| row.wake).unwrap();
                self.tail = all[wake..].to_vec();
            }
        }
        self.steps = self.process.clone();
        if let Some(index) = self.process.iter().rposition(Row::assistant) {
            self.answer = Some(self.process.remove(index));
        }
        self
    }

    fn timestamp(&self, last: bool) -> Option<i128> {
        let source = if last {
            self.steps.iter().chain(&self.trailing).collect::<Vec<_>>()
        } else {
            self.users.iter().chain(&self.steps).collect::<Vec<_>>()
        };
        let timestamps = source
            .into_iter()
            .filter_map(|row| if last { row.last_ts() } else { row.first_ts() });
        if last { timestamps.max() } else { timestamps.min() }
    }

    fn json(&self) -> Value {
        let ids = |rows: &[Row]| rows.iter().map(|row| Value::String(row.id.clone())).collect::<Vec<_>>();
        let after_wake = self.wakes.first().and_then(|wake| {
            self.ordered
                .iter()
                .position(|row| row.id == wake.id)
                .map(|index| ids(&self.ordered[index..]))
        });
        serde_json::json!({
            "id": self.id,
            "user_ids": ids(&self.users),
            "process_ids": ids(&self.process),
            "answer_id": self.answer.as_ref().map(|row| &row.id),
            "trailing_ids": ids(&self.trailing),
            "wake_ids": ids(&self.wakes),
            "after_wake_ids": after_wake.unwrap_or_default().into_iter().filter(|id| {
                let id = id.as_str().unwrap_or_default();
                !self.users.iter().any(|row| row.id == id) && !id.starts_with("message#")
            }).collect::<Vec<_>>(),
            "tail_ids": ids(&self.tail),
            "ordered_ids": ids(&self.ordered),
            "tool_count": self.process.iter().map(|row| row.tools).sum::<i64>(),
            "thought_count": self.process.iter().map(|row| row.thoughts).sum::<i64>()
                + self.process.iter().filter(|row| row.kind == Kind::Thinking).count() as i64,
            "interim_count": self.process.iter().filter(|row| row.assistant()).count(),
            "start": self.timestamp(false).and_then(timestamp_text),
            "end": self.timestamp(true).and_then(timestamp_text),
        })
    }
}

fn part(id: &str) -> i64 {
    id.rsplit_once('#').and_then(|(_, part)| part.parse().ok()).unwrap_or(0)
}

/// Replays one static `lines` input through the row and turn projections. Fixture operation sequences and the rendered
/// `ChatLine` / `TaskIndex` projections are deliberately outside this slice.
pub fn project(input: &Input) -> Value {
    let rows = rows(&input.items);
    let rows = input.frozen.as_ref().map_or(rows.clone(), |frozen| {
        rows.into_iter()
            .filter(|row| {
                input.items.iter().any(|item| {
                    item.id == row.id.strip_prefix("tools-").unwrap_or(&row.id)
                        && (item.seq, part(&item.id)) <= (frozen.seq, frozen.part)
                })
            })
            .collect()
    });
    let mut current: Option<Turn> = None;
    let mut last_raw: Option<Turn> = None;
    let mut last_index = 0_usize;
    let mut reopened_at = None;
    let mut message_entry = None;
    let mut supervision_round = false;
    let mut blocks: Vec<Value> = Vec::new();
    let close = |current: &mut Option<Turn>,
                 last_raw: &mut Option<Turn>,
                 last_index: &mut usize,
                 reopened_at: &mut Option<usize>,
                 blocks: &mut Vec<Value>| {
        let Some(turn) = current.take() else { return };
        *last_raw = Some(turn.clone());
        let turn = turn.close();
        if let Some(index) = reopened_at.take() {
            blocks[index] = turn.json();
            *last_index = index;
        } else {
            *last_index = blocks.len();
            blocks.push(turn.json());
        }
    };
    for row in rows {
        if row.user_input() {
            let same = current
                .as_ref()
                .and_then(|turn| turn.users.last())
                .is_some_and(|previous| previous.kind == Kind::User)
                && row.entry == message_entry;
            if let Some(turn) = current
                .as_mut()
                .filter(|_| same || (row.queued && !row.supervision && !supervision_round))
            {
                if row.kind == Kind::User {
                    turn.users.push(row.clone());
                } else {
                    turn.process.push(row.clone());
                }
                turn.ordered.push(row.clone());
            } else {
                close(
                    &mut current,
                    &mut last_raw,
                    &mut last_index,
                    &mut reopened_at,
                    &mut blocks,
                );
                current = Some(Turn {
                    id: row.id.clone(),
                    users: vec![row.clone()],
                    ordered: vec![row.clone()],
                    ..Turn::default()
                });
            }
            if !same {
                supervision_round = row.supervision;
            }
            message_entry = row.entry.clone();
            continue;
        }
        match row.kind {
            Kind::Recap => {
                close(
                    &mut current,
                    &mut last_raw,
                    &mut last_index,
                    &mut reopened_at,
                    &mut blocks,
                );
                blocks.push(Value::Null);
            }
            Kind::Notice => {
                if row.wake
                    && current.is_none()
                    && let Some(raw) = &last_raw
                    && blocks
                        .get(last_index)
                        .and_then(Value::as_object)
                        .and_then(|turn| turn.get("id"))
                        == Some(&Value::String(raw.id.clone()))
                {
                    current = Some(raw.clone());
                    reopened_at = Some(last_index);
                }
                if row.refusal {
                    let turn = current.get_or_insert_with(|| Turn {
                        id: row.id.clone(),
                        ..Turn::default()
                    });
                    turn.trailing.push(row);
                } else if row.wake && current.is_some() {
                    let turn = current.as_mut().unwrap();
                    turn.wakes.push(row.clone());
                    turn.process.push(row.clone());
                    turn.ordered.push(row);
                } else if current.is_none() {
                    current = Some(Turn {
                        id: row.id.clone(),
                        process: vec![row.clone()],
                        ordered: vec![row],
                        ..Turn::default()
                    });
                } else if let Some(turn) = current.as_mut() {
                    if row.interrupt {
                        turn.trailing.push(row);
                    } else {
                        turn.process.push(row.clone());
                        turn.ordered.push(row);
                    }
                }
            }
            _ => {
                let turn = current.get_or_insert_with(|| Turn {
                    id: row.id.clone(),
                    ..Turn::default()
                });
                turn.process.push(row.clone());
                turn.ordered.push(row);
            }
        }
    }
    close(
        &mut current,
        &mut last_raw,
        &mut last_index,
        &mut reopened_at,
        &mut blocks,
    );
    Value::Array(blocks.into_iter().filter(|block| !block.is_null()).collect())
}

/// Wake selection mutation: choosing the first assistant row must disagree with the fixture corpus.
#[doc(hidden)]
pub fn answer_mutation(input: &Input) -> Option<String> {
    let turns = project(input);
    turns
        .as_array()?
        .first()?
        .as_object()?
        .get("ordered_ids")?
        .as_array()?
        .iter()
        .find_map(|id| {
            let id = id.as_str()?;
            input
                .items
                .iter()
                .find(|item| item.id == id && item.kind == "text" && item.role == "assistant")
                .map(|item| item.id.clone())
        })
}

fn timestamp_text(nanoseconds: i128) -> Option<String> {
    let seconds = nanoseconds.div_euclid(1_000_000_000);
    let days = seconds.div_euclid(86_400);
    let time = seconds.rem_euclid(86_400);
    let (year, month, day) = civil_from_days(days);
    let fraction = nanoseconds.rem_euclid(1_000_000_000);
    let mut result = format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}",
        time / 3600,
        time % 3600 / 60,
        time % 60
    );
    if fraction != 0 {
        result += &format!(".{fraction:09}");
        while result.ends_with('0') {
            result.pop();
        }
    }
    Some(result + "Z")
}

// Howard Hinnant's inverse civil calendar calculation, with 1970-01-01 as day zero.
fn civil_from_days(days: i128) -> (i128, i128, i128) {
    let days = days + 719_468;
    let era = days.div_euclid(146_097);
    let day_of_era = days - era * 146_097;
    let year_of_era = (day_of_era - day_of_era / 1460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let mut year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_prime = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * month_prime + 2) / 5 + 1;
    let month = month_prime + if month_prime < 10 { 3 } else { -9 };
    year += i128::from(month <= 2);
    (year, month, day)
}
