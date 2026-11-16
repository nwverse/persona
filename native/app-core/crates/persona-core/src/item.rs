//! A chat item as the hub sends it (`docs/protocol/protocol.md`, "Item continuity").
//!
//! The core reads only the fields its rules use and keeps the item's JSON exactly as received, so the clients render
//! what the hub sent and nothing is lost in a round trip through the core.

mod head;

use std::borrow::Cow;

use serde_json::value::RawValue;

use self::head::{CacheHead, CacheItemHead, CacheSeen, FoldValue, ItemFold, ItemHead, fold_value};
use crate::transport::seen::{KeyFault, opens_object};

/// The closed range of work a fold item stands for, from the fold's own position through `through_seq` /
/// `through_id`.
#[derive(Debug, Clone, PartialEq)]
pub struct FoldRange {
    pub through_seq: i64,
    pub through_id: String,
    /// The fold's whole object, compared when the same fold id arrives again.
    value: serde_json::Value,
}

#[derive(Debug, Clone)]
pub struct Item {
    pub id: String,
    pub seq: i64,
    pub kind: String,
    pub role: String,
    pub tool_use_id: Option<String>,
    pub fold: Option<FoldRange>,
    pub supervision_user: bool,
    fold_supervision_user: bool,
    /// The item's JSON as the hub sent it.
    raw: Box<RawValue>,
}

/// Why an item of a hub payload was refused. It names keys of the protocol only, never the item's content.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ItemFault {
    /// The item is not a JSON object.
    NotObject,
    /// The item is JSON, but the parser cannot hold part of what the core reads from it: a number beyond a double's
    /// range or an escape that is not a character (a lone surrogate) in a key the core reads or in the name of any
    /// key of the item, or a `fold` that nests deeper than 128 levels or holds such a number or escape anywhere.
    Unreadable,
    /// A key of the item, or `fold.<key>` for a key of its fold.
    Key(KeyFault),
}

impl From<KeyFault> for ItemFault {
    fn from(fault: KeyFault) -> Self {
        ItemFault::Key(fault)
    }
}

/// A cached or fixture item the core could not read. The message is a code and a position only: serde's own error
/// text quotes the offending value, which may be a message's words, and these errors reach a client's diagnostics.
#[derive(Debug)]
pub struct ItemError(String);

impl ItemError {
    fn at(context: &str, error: &serde_json::Error) -> Self {
        ItemError(format!("{context}: line {} column {}", error.line(), error.column()))
    }
}

impl std::fmt::Display for ItemError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for ItemError {}

impl Item {
    /// Reads a cached item. The object form and serde's historical positional form share the same head/fold slots;
    /// only id is required. Other fields retain their legacy defaults and type/null behavior.
    pub fn from_raw(raw: Box<RawValue>) -> Result<Item, ItemError> {
        let CacheItemHead(CacheHead {
            id,
            seq,
            kind,
            role,
            tool_use_id,
            fold,
            supervision_user,
            ..
        }) = serde_json::from_str(raw.get()).map_err(|e| ItemError::at("item", &e))?;
        let id = match id {
            CacheSeen::Value(id) => id,
            CacheSeen::Missing => unreachable!("the cache reader requires id"),
        };
        let (fold, fold_supervision_user) = match fold.into_option().unwrap_or(FoldValue::Absent) {
            FoldValue::Absent => (None, false),
            FoldValue::Value(value) => {
                let supervision = value.get("supervision_user").and_then(serde_json::Value::as_bool) == Some(true);
                (
                    Some(FoldRange {
                        through_seq: value
                            .get("through_seq")
                            .and_then(serde_json::Value::as_i64)
                            .unwrap_or(0),
                        through_id: value
                            .get("through_id")
                            .and_then(serde_json::Value::as_str)
                            .unwrap_or("")
                            .to_owned(),
                        value,
                    }),
                    supervision,
                )
            }
        };
        Ok(Item {
            id,
            seq: seq.into_option().unwrap_or_default(),
            kind: kind.into_option().flatten().unwrap_or_else(|| "unknown".to_owned()),
            role: role.into_option().flatten().unwrap_or_default(),
            tool_use_id: tool_use_id.into_option().flatten(),
            fold,
            supervision_user: supervision_user.into_option().unwrap_or_default(),
            fold_supervision_user,
            raw,
        })
    }

    /// Reads one item of a hub payload, refusing what iOS's `Item` refuses among the keys listed below. The JSON is
    /// copied once and kept as received.
    ///
    /// Enforced, as iOS's synthesized `Item.init(from:)` does (`apple/Shared/Models.swift`, `Item`, `Fold`):
    ///
    /// - `id`, `role` and `kind` are required strings, `seq` a required whole number;
    /// - `text`, `tool_use_id`, `tool_name`, `from` and `client_msg_id` are a string, `null` or absent;
    /// - `is_error`, `queued`, `wake` and `supervision_user` are a boolean, `null` or absent;
    /// - `full_size` is a whole number, `null` or absent;
    /// - `fold` is `null`, absent, or an object whose `items`, `tools`, `thoughts` and `through_seq` are required
    ///   whole numbers, `through_id` a required string and `supervision_user` a boolean, `null` or absent.
    ///
    /// A key that appears more than once keeps its first value, in the item and in its fold, as Foundation's keyed
    /// container does.
    ///
    /// **Not replicated**: iOS also refuses an item whose `image`, `task`, `compact`, `injected` or `model_refusal`
    /// is present but does not decode as its struct, a fold whose `images` or `tasks` do not, and an item whose
    /// `tool_input` or `source` holds a number beyond a double's range. The core does not read those keys and
    /// accepts such an item. Unlike iOS, the core keeps an unknown `kind` as the hub wrote it rather than as
    /// `unknown`; no rule of the core tells the two apart. `transport`'s module documentation has the full list.
    pub fn from_wire(raw: &RawValue) -> Result<Item, ItemFault> {
        let json = raw.get();
        if !opens_object(json) {
            return Err(ItemFault::NotObject);
        }
        let head: ItemHead<'_> = serde_json::from_str(json).map_err(|_| ItemFault::Unreadable)?;
        let id = head.id.required("id")?;
        let seq = head.seq.required("seq")?;
        let role = head.role.required("role")?;
        let kind = head.kind.required("kind")?;
        let tool_use_id = head.tool_use_id.optional("tool_use_id")?;
        head.text.optional("text")?;
        head.tool_name.optional("tool_name")?;
        head.from.optional("from")?;
        head.client_msg_id.optional("client_msg_id")?;
        head.is_error.optional("is_error")?;
        head.queued.optional("queued")?;
        head.wake.optional("wake")?;
        let supervision_user = head.supervision_user.optional("supervision_user")? == Some(true);
        head.full_size.optional("full_size")?;
        let mut fold_supervision_user = false;
        let fold = match head.fold.0.map(RawValue::get) {
            None | Some("null") => None,
            Some(json) => {
                if !opens_object(json) {
                    return Err(KeyFault::WrongType("fold").into());
                }
                let fold: ItemFold<'_> = serde_json::from_str(json).map_err(|_| ItemFault::Unreadable)?;
                fold.items.required("fold.items")?;
                fold.tools.required("fold.tools")?;
                fold.thoughts.required("fold.thoughts")?;
                let through_seq = fold.through_seq.required("fold.through_seq")?;
                let through_id = fold.through_id.required("fold.through_id")?.into_owned();
                fold_supervision_user = fold.supervision_user.optional("fold.supervision_user")? == Some(true);
                Some(FoldRange {
                    through_seq,
                    through_id,
                    value: fold_value(json)
                        .map_err(|_| ItemFault::Unreadable)?
                        .expect("a fold object is a value"),
                })
            }
        };
        Ok(Item {
            id: id.into_owned(),
            seq,
            kind: kind.into_owned(),
            role: role.into_owned(),
            tool_use_id: tool_use_id.map(Cow::into_owned),
            fold,
            supervision_user,
            fold_supervision_user,
            raw: raw.to_owned(),
        })
    }

    /// Reads a JSON array of items.
    pub fn list_from_json(json: &str) -> Result<Vec<Item>, ItemError> {
        let raws: Vec<Box<RawValue>> = serde_json::from_str(json).map_err(|e| ItemError::at("items", &e))?;
        raws.into_iter().map(Item::from_raw).collect()
    }

    /// The item's JSON exactly as the hub sent it.
    pub fn json(&self) -> &str {
        self.raw.get()
    }

    /// Authoritative visibility evidence survives a later copy that omits it, as iOS's merge requires.
    pub fn has_supervision_evidence(&self) -> bool {
        self.supervision_user || self.fold_supervision_user
    }

    pub fn with_supervision(mut self) -> Self {
        self.supervision_user = true;
        self.fold_supervision_user = true;
        if let Ok(mut value) = serde_json::from_str::<serde_json::Value>(self.raw.get()) {
            value["supervision_user"] = serde_json::Value::Bool(true);
            if let Some(fold) = value.get_mut("fold").filter(|fold| fold.is_object()) {
                fold["supervision_user"] = serde_json::Value::Bool(true);
            }
            if let Ok(raw) = serde_json::value::to_raw_value(&value) {
                self.raw = raw;
            }
        }
        self
    }

    /// The number after the last `#` of the id: the block within one transcript line. Ids without one count as 0.
    pub fn part(&self) -> i64 {
        part(&self.id)
    }

    /// Work a fold can stand for: thinking, tools, folds, the assistant's narration and a tool's images. The
    /// assistant's text outside a fold's range is the answer and stays.
    pub fn is_work(&self) -> bool {
        match self.kind.as_str() {
            "thinking" | "tool_use" | "tool_result" | "fold" => true,
            "text" => self.role == "assistant",
            "image" => self.tool_use_id.is_some(),
            _ => false,
        }
    }

    /// Whether this fold covers `other`: from the fold's position through its `through_seq` / `through_id`, both ends
    /// included, comparing the seq and then the block number.
    pub fn folds(&self, other: &Item) -> bool {
        let Some(range) = &self.fold else { return false };
        let start_part = part(&self.id);
        let other_part = other.part();
        (other.seq > self.seq || other.seq == self.seq && other_part >= start_part)
            && (other.seq < range.through_seq
                || other.seq == range.through_seq && other_part <= part(&range.through_id))
    }

    /// The same fold arrived again unchanged.
    pub(crate) fn same_fold(&self, other: &Item) -> bool {
        match (&self.fold, &other.fold) {
            (Some(a), Some(b)) => a.value == b.value,
            (None, None) => true,
            _ => false,
        }
    }

    /// Ordering key of a chat's items.
    pub(crate) fn key(&self) -> (i64, i64) {
        (self.seq, self.part())
    }
}

pub(crate) fn part(id: &str) -> i64 {
    id.rsplit_once('#').and_then(|(_, n)| n.parse().ok()).unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_item_error_never_repeats_the_payload() {
        let secret = "SYNTHETIC-MESSAGE-TEXT";
        let bad = format!(r#"[{{"id":"a#0","seq":"{secret}"}}]"#);
        let error = Item::list_from_json(&bad).expect_err("a wrong-typed seq");
        assert!(
            !error.to_string().contains(secret),
            "the error must not quote the value: {error}"
        );
        assert!(error.to_string().starts_with("item: line "), "{error}");
        // `from_raw` reads the head with serde; a missing required id is refused, and its error carries a position,
        // not the item's words.
        let raw = serde_json::value::RawValue::from_string(format!(r#"{{"seq":1,"text":"{secret}"}}"#)).unwrap();
        let error = Item::from_raw(raw).expect_err("an item without an id");
        assert!(!error.to_string().contains(secret), "{error}");
        assert!(error.to_string().starts_with("item: line "), "{error}");
    }

    #[test]
    fn parts_and_defaults() {
        assert_eq!(part("12#3"), 3);
        assert_eq!(part("fold:2#0"), 0);
        assert_eq!(part("plain"), 0);
        let item = Item::list_from_json(r#"[{"id":"a"}]"#).unwrap().remove(0);
        assert_eq!((item.seq, item.kind.as_str(), item.role.as_str()), (0, "unknown", ""));
        assert_eq!(item.json(), r#"{"id":"a"}"#);
    }

    #[test]
    fn cache_repeated_keys_follow_the_wire_without_losing_legacy_forms() {
        let json = r#"{"id":"a#1","id":"b#2","seq":1,"seq":"wrong","role":"assistant","kind":"fold","kind":"text","tool_use_id":null,"tool_use_id":"later","supervision_user":true,"supervision_user":false,"fold":{"items":1,"tools":0,"thoughts":0,"through_seq":2,"through_seq":9,"through_id":"x#1","through_id":"y#2","supervision_user":null,"supervision_user":true},"fold":null}"#;
        let cached = Item::list_from_json(&format!("[{json}]")).unwrap().remove(0);
        let wire = Item::from_wire(&RawValue::from_string(json.to_owned()).unwrap()).unwrap();
        for item in [&cached, &wire] {
            assert_eq!((item.id.as_str(), item.seq, item.kind.as_str()), ("a#1", 1, "fold"));
            assert_eq!(item.tool_use_id, None);
            assert!(item.supervision_user);
            assert!(!item.fold_supervision_user, "the first null does not fall through");
            let fold = item.fold.as_ref().expect("the first fold");
            assert_eq!((fold.through_seq, fold.through_id.as_str()), (2, "x#1"));
            assert_eq!(item.json(), json, "the original bytes are never normalized");
        }
        assert!(cached.same_fold(&wire), "both paths normalize the same fold tree");
        let expected = serde_json::json!({
            "items": 1, "tools": 0, "thoughts": 0,
            "through_seq": 2, "through_id": "x#1", "supervision_user": null,
        });
        assert_eq!(cached.fold.as_ref().unwrap().value, expected);
        assert_eq!(wire.fold.as_ref().unwrap().value, expected);

        // An unrelated repeated head key cannot change which fold is selected or any of its normalized evidence.
        for prefix in [
            r#""id":"a#1","id":"other","#,
            r#""id":"a#1","unknown":{"nested":true},"unknown":null,"#,
        ] {
            let item = Item::list_from_json(&format!(
                "[{{{prefix}\"seq\":1,\"kind\":\"fold\",\"fold\":{{\"through_seq\":2,\"through_seq\":9,\"through_id\":\"x#1\",\"through_id\":\"y#2\",\"supervision_user\":true,\"supervision_user\":false}}}}]"
            ))
            .unwrap()
            .remove(0);
            let fold = item.fold.as_ref().unwrap();
            assert_eq!((fold.through_seq, fold.through_id.as_str()), (2, "x#1"));
            assert!(item.fold_supervision_user);
            assert!(item.has_supervision_evidence());
        }

        let null_fold = r#"{"id":"a","fold":null,"fold":{"through_seq":8,"through_id":"b"}}"#;
        assert!(
            Item::list_from_json(&format!("[{null_fold}]")).unwrap()[0]
                .fold
                .is_none()
        );
        // Legacy positional cache entries retain their field order, defaults and byte-for-byte source.
        let positional =
            r#"["a",2,"kind","role","tool",{"through_seq":1,"through_seq":9,"through_id":"a","through_id":"b"},true]"#;
        let item = Item::list_from_json(&format!("[{positional}]")).unwrap().remove(0);
        assert_eq!((item.id.as_str(), item.seq, item.kind.as_str()), ("a", 2, "kind"));
        assert_eq!(
            (item.role.as_str(), item.tool_use_id.as_deref()),
            ("role", Some("tool"))
        );
        assert_eq!(
            item.fold.as_ref().map(|f| (f.through_seq, f.through_id.as_str())),
            Some((1, "a"))
        );
        assert!(item.supervision_user);
        assert_eq!(item.json(), positional);
        let minimal = Item::list_from_json(r#"[["a"]]"#).unwrap().remove(0);
        assert_eq!(
            (minimal.id.as_str(), minimal.seq, minimal.kind.as_str()),
            ("a", 0, "unknown")
        );
    }

    #[test]
    fn cache_legacy_errors_keep_their_positions() {
        for (json, expected) in [
            (r#"[]"#, "item: line 1 column 2"),
            (r#"{"seq":1}"#, "item: line 1 column 9"),
            (r#"{"id":null}"#, "item: line 1 column 10"),
            (r#"{"id":1}"#, "item: line 1 column 7"),
            (r#"{"id":"a","seq":null}"#, "item: line 1 column 20"),
            (r#"{"id":"a","seq":1.0}"#, "item: line 1 column 19"),
            (r#"{"id":"a","kind":1}"#, "item: line 1 column 18"),
            (r#"{"id":"a","supervision_user":null}"#, "item: line 1 column 33"),
            (r#"{"id":"a","supervision_user":1}"#, "item: line 1 column 30"),
            (r#"["a",null]"#, "item: line 1 column 9"),
            (
                r#"["a",2,"kind","role","tool",null,false,"extra"]"#,
                "item: line 1 column 40",
            ),
        ] {
            let error = Item::list_from_json(&format!("[{json}]")).expect_err(json);
            assert_eq!(error.to_string(), expected, "{json}");
        }
        // Errors inside a snapshot array keep the `items` context, not the wire's protocol-key error vocabulary.
        let error = Item::list_from_json(r#"[{"id":"a"},{"id":1}]"#).unwrap_err();
        assert_eq!(error.to_string(), "item: line 1 column 7");
        let error = Item::list_from_json("[").unwrap_err();
        assert_eq!(error.to_string(), "items: line 1 column 1");
    }

    #[test]
    fn cache_repeated_and_unknown_values_retain_the_recursion_boundary() {
        let deep = format!("{}{}", "[".repeat(200_000), "]".repeat(200_000));
        let json = format!(r#"{{"id":"a","unknown":{deep},"unknown":1,"seq":0,"seq":{deep}}}"#);
        let item = Item::list_from_json(&format!("[{json}]")).unwrap().remove(0);
        assert_eq!(item.json(), json, "deep ignored values are skipped without recursion");
        // The selected fold is the one bounded tree, as it was before this shared reader.
        let deep = format!("{}{}", "[".repeat(128), "]".repeat(128));
        let json = format!(r#"{{"id":"a","fold":{{"x":{deep}}}}}"#);
        let error = Item::list_from_json(&format!("[{json}]")).unwrap_err();
        assert_eq!(error.to_string(), "item: line 1 column 148");
        let json = format!(r#"{{"id":"a","fold":{deep}}}"#);
        let error = Item::list_from_json(&format!("[{json}]")).unwrap_err();
        assert_eq!(error.to_string(), "item: line 1 column 144");
    }
}
