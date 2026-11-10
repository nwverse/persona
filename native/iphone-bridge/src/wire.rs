//! The JSON lines the node and the bridge exchange (docs/extensions/iphone-bridge.md).

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};

use crate::error::BridgeError;

pub const WIRE_VERSION: u64 = 1;
pub const DEFAULT_PORT: u16 = 49152;
pub const DEFAULT_TIMEOUT_MS: u64 = 30_000;
pub const MIN_TIMEOUT_MS: u64 = 1_000;
pub const MAX_TIMEOUT_MS: u64 = 120_000;
/// Longest request line `call` and `serve` accept.
pub const MAX_LINE: usize = 1 << 20;

pub const OPS: &[&str] = &[
    "device_info",
    "screenshot",
    "ax_tree",
    "tap",
    "long_press",
    "drag",
    "swipe",
    "keys",
    "button",
    "launch_app",
    "terminate_app",
    "list_apps",
];

/// Where the phone is and how to prove who we are to it.
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct DeviceSpec {
    pub udid: String,
    pub address: String,
    #[serde(default)]
    pub port: Option<u16>,
    pub pairing_file: String,
}

impl DeviceSpec {
    pub fn port(&self) -> u16 {
        self.port.unwrap_or(DEFAULT_PORT)
    }
}

/// One request, as read off a line.
#[derive(Debug, Clone, PartialEq)]
pub struct Request {
    pub id: Option<String>,
    pub op: String,
    pub device: Option<DeviceSpec>,
    pub timeout_ms: u64,
    pub args: Map<String, Value>,
}

#[derive(Deserialize)]
struct RawRequest {
    #[serde(default)]
    v: Option<Value>,
    #[serde(default)]
    id: Option<Value>,
    #[serde(default)]
    op: Option<String>,
    #[serde(default)]
    device: Option<DeviceSpec>,
    #[serde(default)]
    timeout_ms: Option<u64>,
    #[serde(default)]
    args: Option<Value>,
}

/// The id and op of a request line, as far as they can be read, so an error about it can still name them.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Echo {
    pub id: Option<String>,
    pub op: Option<String>,
}

/// Parses one request line. `require_v` / `require_id` follow the mode (`call` needs `v`, `serve` needs `id`).
pub fn parse_request(line: &str, require_v: bool, require_id: bool) -> Result<Request, (Echo, BridgeError)> {
    let value: Value = serde_json::from_str(line.trim())
        .map_err(|e| (Echo::default(), BridgeError::bad_request(format!("not JSON: {e}"))))?;
    let echo = Echo {
        id: value.get("id").and_then(id_string),
        op: value.get("op").and_then(Value::as_str).map(str::to_owned),
    };
    let fail = |msg: String| (echo.clone(), BridgeError::bad_request(msg));
    if !value.is_object() {
        return Err(fail("a request is a JSON object".into()));
    }
    let raw: RawRequest = serde_json::from_value(value.clone()).map_err(|e| fail(format!("invalid request: {e}")))?;
    match raw.v.as_ref().map(Value::as_u64) {
        None if require_v => return Err(fail("missing v".into())),
        None => {}
        Some(Some(WIRE_VERSION)) => {}
        Some(_) => return Err(fail(format!("unsupported wire version {}", raw.v.unwrap_or_default()))),
    }
    let id = match raw.id {
        None | Some(Value::Null) => None,
        Some(v) => Some(id_string(&v).ok_or_else(|| fail("id must be a string".into()))?),
    };
    if require_id && id.is_none() {
        return Err(fail("missing id".into()));
    }
    let op = raw.op.ok_or_else(|| fail("missing op".into()))?;
    if !OPS.contains(&op.as_str()) {
        return Err(fail(format!("unknown op {op:?}")));
    }
    let args = match raw.args {
        None | Some(Value::Null) => Map::new(),
        Some(Value::Object(m)) => m,
        Some(_) => return Err(fail("args must be an object".into())),
    };
    Ok(Request {
        id,
        op,
        device: raw.device,
        timeout_ms: clamp_timeout(raw.timeout_ms),
        args,
    })
}

fn id_string(v: &Value) -> Option<String> {
    v.as_str().map(str::to_owned)
}

pub fn clamp_timeout(ms: Option<u64>) -> u64 {
    ms.unwrap_or(DEFAULT_TIMEOUT_MS).clamp(MIN_TIMEOUT_MS, MAX_TIMEOUT_MS)
}

/// A successful answer line.
#[derive(Debug, Serialize)]
pub struct Answer<'a> {
    pub v: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<&'a str>,
    pub ok: bool,
    pub op: &'a str,
    pub elapsed_ms: u64,
    pub result: Value,
}

pub fn answer_line(id: Option<&str>, op: &str, elapsed_ms: u64, result: Value) -> String {
    serde_json::to_string(&Answer {
        v: WIRE_VERSION,
        id,
        ok: true,
        op,
        elapsed_ms,
        result,
    })
    .expect("an answer always serializes")
}

/// An error line: the last line of stderr for one-shot commands, an answer line in `serve`.
pub fn error_line(id: Option<&str>, op: Option<&str>, err: &BridgeError) -> String {
    let mut m = Map::new();
    m.insert("v".into(), json!(WIRE_VERSION));
    if let Some(id) = id {
        m.insert("id".into(), json!(id));
    }
    m.insert("ok".into(), json!(false));
    if let Some(op) = op {
        m.insert("op".into(), json!(op));
    }
    m.insert(
        "error".into(),
        json!({"code": err.code.as_str(), "message": err.message}),
    );
    Value::Object(m).to_string()
}

pub fn event_line(event: &str, extra: Map<String, Value>) -> String {
    let mut m = Map::new();
    m.insert("event".into(), json!(event));
    m.extend(extra);
    Value::Object(m).to_string()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::error::Code;

    const CALL: &str = r#"{"v":1,"id":"c-1","op":"tap","device":{"udid":"U","address":"192.0.2.1","pairing_file":"/p"},"args":{"x":1,"y":2}}"#;

    #[test]
    fn parses_a_call_request() {
        let r = parse_request(CALL, true, false).unwrap();
        assert_eq!(r.id.as_deref(), Some("c-1"));
        assert_eq!(r.op, "tap");
        assert_eq!(r.timeout_ms, DEFAULT_TIMEOUT_MS);
        let d = r.device.unwrap();
        assert_eq!(d.port(), DEFAULT_PORT);
        assert_eq!(r.args["x"], json!(1));
    }

    #[test]
    fn clamps_the_timeout() {
        assert_eq!(clamp_timeout(Some(5)), MIN_TIMEOUT_MS);
        assert_eq!(clamp_timeout(Some(10_000_000)), MAX_TIMEOUT_MS);
        assert_eq!(clamp_timeout(Some(4_000)), 4_000);
    }

    #[test]
    fn serve_needs_an_id_but_not_v() {
        let err = parse_request(r#"{"op":"tap"}"#, false, true).unwrap_err();
        assert_eq!(err.1.code, Code::BadRequest);
        let r = parse_request(r#"{"id":"a","op":"screenshot"}"#, false, true).unwrap();
        assert!(r.device.is_none());
        assert!(r.args.is_empty());
    }

    #[test]
    fn rejects_bad_requests_with_what_could_be_read() {
        let (echo, err) = parse_request(r#"{"v":2,"id":"x","op":"tap"}"#, true, false).unwrap_err();
        assert_eq!(err.code, Code::BadRequest);
        assert_eq!(echo.id.as_deref(), Some("x"));
        assert_eq!(echo.op.as_deref(), Some("tap"));
        assert!(parse_request(r#"{"v":1,"op":"format_disk"}"#, true, false).is_err());
        assert!(parse_request(r#"{"v":1,"op":"tap","args":[1]}"#, true, false).is_err());
        assert!(parse_request("not json", true, false).is_err());
        assert!(parse_request(r#"{"op":"tap"}"#, true, false).is_err());
        assert!(parse_request("[1]", false, false).is_err());
    }

    #[test]
    fn ignores_unknown_fields() {
        let r = parse_request(r#"{"v":1,"op":"device_info","later":true}"#, true, false).unwrap();
        assert_eq!(r.op, "device_info");
    }

    #[test]
    fn writes_answer_and_error_lines() {
        let a: Value = serde_json::from_str(&answer_line(Some("c"), "tap", 3, json!({}))).unwrap();
        assert_eq!(
            a,
            json!({"v":1,"id":"c","ok":true,"op":"tap","elapsed_ms":3,"result":{}})
        );
        let e: Value =
            serde_json::from_str(&error_line(None, Some("tap"), &BridgeError::new(Code::Timeout, "slow"))).unwrap();
        assert_eq!(
            e,
            json!({"v":1,"ok":false,"op":"tap","error":{"code":"timeout","message":"slow"}})
        );
        assert!(!answer_line(None, "tap", 0, json!({})).contains('\n'));
    }
}
