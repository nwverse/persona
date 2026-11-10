//! Pairing over USB (`usb-list`, `pair`) and matching a Bonjour advertisement to a record (`match`): written in
//! #3448, which owns creating pairing records. Until then each answers `unsupported`. The record file format is
//! `record::Record`.

use std::path::Path;

use serde_json::Value;

use crate::error::{BridgeError, Code};

fn not_yet(what: &str) -> BridgeError {
    BridgeError::new(Code::Unsupported, format!("{what} is not implemented yet"))
}

/// Phones attached over USB: `[{"udid", "name", "product_version", "model"}]`.
pub async fn usb_list() -> Result<Value, BridgeError> {
    Err(not_yet("usb-list"))
}

/// Pairs with the phone `udid` over trusted USB and writes the record to `out`.
pub async fn pair(udid: &str, out: &Path) -> Result<Value, BridgeError> {
    let _ = (udid, out);
    Err(not_yet("pair"))
}

/// Whether a `_remotepairing._tcp` advertisement (its TXT `identifier` and `authTag`) is the phone of the
/// record at `record`: `{"match": true|false}`.
pub async fn match_record(record: &Path, identifier: &str, auth_tag: &str) -> Result<Value, BridgeError> {
    let _ = (record, identifier, auth_tag);
    Err(not_yet("match"))
}
