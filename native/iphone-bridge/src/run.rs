//! `call` and `serve`: reading requests, running them under their deadlines, writing answers.

use std::time::Duration;

use tokio::time::Instant;

use serde_json::{Map, Value, json};
use tokio::io::{AsyncBufRead, AsyncBufReadExt, AsyncWrite, AsyncWriteExt};

use crate::error::{BridgeError, Code};
use crate::ops::{self, Action};
use crate::wire::{self, DeviceSpec, MAX_LINE};

/// A connected phone. The real one is `device::Device`; tests use a stand-in.
#[allow(async_fn_in_trait)]
pub trait Phone {
    async fn run(&mut self, action: Action) -> Result<Value, BridgeError>;
    /// When `idle` should be called, if something is held open until then.
    fn idle_at(&self) -> Option<Instant>;
    /// Nothing happened until `idle_at`: let go of what was held open for the next operation.
    async fn idle(&mut self);
    /// The process is about to end normally.
    async fn close(&mut self);
}

/// What a command ends with: exit code, and the error line for stderr if it failed.
#[derive(Debug, PartialEq)]
pub struct Outcome {
    pub code: i32,
    pub stderr_error: Option<String>,
}

impl Outcome {
    fn ok() -> Self {
        Self {
            code: 0,
            stderr_error: None,
        }
    }

    fn failed(id: Option<&str>, op: Option<&str>, err: &BridgeError) -> Self {
        Self {
            code: 1,
            stderr_error: Some(wire::error_line(id, op, err)),
        }
    }
}

enum Line {
    Text(String),
    Eof,
}

/// Reads one line of at most `MAX_LINE` bytes into `buf`, returning it without its newline. Cancel-safe:
/// what was read before a cancellation stays in `buf` for the next call. With `whole_lines`, a last line cut
/// off by end of input counts as never sent: the node closes stdin when a write fails halfway and may send
/// the request again on a new connection.
async fn read_line<R: AsyncBufRead + Unpin>(
    input: &mut R,
    buf: &mut Vec<u8>,
    whole_lines: bool,
) -> Result<Line, BridgeError> {
    let too_long = || BridgeError::bad_request(format!("request line longer than {MAX_LINE} bytes"));
    loop {
        let avail = input
            .fill_buf()
            .await
            .map_err(|e| BridgeError::internal(format!("read stdin: {e}")))?;
        if avail.is_empty() {
            let rest = std::mem::take(buf);
            if rest.is_empty() || whole_lines {
                return Ok(Line::Eof);
            }
            return text(rest);
        }
        if let Some(i) = avail.iter().position(|&b| b == b'\n') {
            buf.extend_from_slice(&avail[..i]);
            input.consume(i + 1);
            let line = std::mem::take(buf);
            if line.len() > MAX_LINE {
                return Err(too_long());
            }
            return text(line);
        }
        let n = avail.len();
        buf.extend_from_slice(avail);
        input.consume(n);
        if buf.len() > MAX_LINE {
            buf.clear();
            return Err(too_long());
        }
    }
}

fn text(line: Vec<u8>) -> Result<Line, BridgeError> {
    String::from_utf8(line)
        .map(Line::Text)
        .map_err(|_| BridgeError::bad_request("request is not UTF-8"))
}

async fn write_line<W: AsyncWrite + Unpin>(out: &mut W, line: &str) -> Result<(), BridgeError> {
    let mut bytes = Vec::with_capacity(line.len() + 1);
    bytes.extend_from_slice(line.as_bytes());
    bytes.push(b'\n');
    out.write_all(&bytes)
        .await
        .map_err(|e| BridgeError::internal(format!("write stdout: {e}")))?;
    out.flush()
        .await
        .map_err(|e| BridgeError::internal(format!("write stdout: {e}")))
}

fn elapsed_ms(start: Instant) -> u64 {
    start.elapsed().as_millis().try_into().unwrap_or(u64::MAX)
}

fn timed_out(ms: u64) -> BridgeError {
    BridgeError::new(Code::Timeout, format!("no answer within {ms} ms"))
}

/// `call`: one request from `input`, its answer on `out`; connecting is part of the deadline.
pub async fn call<P, R, W, C>(input: &mut R, out: &mut W, connect: C) -> Outcome
where
    P: Phone,
    R: AsyncBufRead + Unpin,
    W: AsyncWrite + Unpin,
    C: AsyncFnOnce(DeviceSpec) -> Result<P, BridgeError>,
{
    let start = Instant::now();
    let line = match read_line(input, &mut Vec::new(), false).await {
        Ok(Line::Text(l)) => l,
        Ok(Line::Eof) => return Outcome::failed(None, None, &BridgeError::bad_request("no request on stdin")),
        Err(e) => return Outcome::failed(None, None, &e),
    };
    let req = match wire::parse_request(&line, true, false) {
        Ok(r) => r,
        Err((echo, e)) => return Outcome::failed(echo.id.as_deref(), echo.op.as_deref(), &e),
    };
    let (id, op) = (req.id.as_deref(), req.op.as_str());
    let action = match ops::parse(op, &req.args) {
        Ok(a) => a,
        Err(e) => return Outcome::failed(id, Some(op), &e),
    };
    let Some(spec) = req.device.clone() else {
        return Outcome::failed(id, Some(op), &BridgeError::bad_request("device: required"));
    };
    let work = async {
        let mut phone = connect(spec).await?;
        let result = phone.run(action).await;
        phone.close().await;
        result
    };
    let result = match tokio::time::timeout(Duration::from_millis(req.timeout_ms), work).await {
        Ok(r) => r,
        Err(_) => Err(timed_out(req.timeout_ms)),
    };
    match result {
        Ok(value) => match write_line(out, &wire::answer_line(id, op, elapsed_ms(start), value)).await {
            Ok(()) => Outcome::ok(),
            Err(e) => Outcome::failed(id, Some(op), &e),
        },
        Err(e) => Outcome::failed(id, Some(op), &e),
    }
}

/// `serve`, once connected: the ready event, then one answer per request line until stdin ends (exit 0) or
/// the link is lost (exit 1).
pub async fn serve<P, R, W>(phone: &mut P, ready: Value, input: &mut R, out: &mut W) -> Outcome
where
    P: Phone,
    R: AsyncBufRead + Unpin,
    W: AsyncWrite + Unpin,
{
    let mut ready_event = Map::new();
    ready_event.insert("device".into(), ready);
    if let Err(e) = write_line(out, &wire::event_line("ready", ready_event)).await {
        return Outcome::failed(None, None, &e);
    }
    let mut buf = Vec::new();
    loop {
        let idle_at = phone.idle_at();
        let line = tokio::select! {
            line = read_line(input, &mut buf, true) => line,
            _ = sleep_until(idle_at), if idle_at.is_some() => {
                phone.idle().await;
                continue;
            }
        };
        let line = match line {
            Ok(Line::Text(l)) => l,
            Ok(Line::Eof) => {
                phone.close().await;
                return Outcome::ok();
            }
            Err(e) => {
                // An unreadable stream: the node's end is broken, nothing more can be matched by id.
                let _ = write_line(out, &wire::error_line(None, None, &e)).await;
                return Outcome::failed(None, None, &e);
            }
        };
        if line.trim().is_empty() {
            continue;
        }
        let start = Instant::now();
        let (id, op, result) = match wire::parse_request(&line, false, true) {
            Err((echo, e)) => (echo.id, echo.op, Err(e)),
            Ok(req) => {
                let result = match ops::parse(&req.op, &req.args) {
                    Err(e) => Err(e),
                    Ok(action) => {
                        match tokio::time::timeout(Duration::from_millis(req.timeout_ms), phone.run(action)).await {
                            Ok(r) => r,
                            Err(_) => Err(timed_out(req.timeout_ms)),
                        }
                    }
                };
                (req.id, Some(req.op), result)
            }
        };
        let (id, op) = (id.as_deref(), op.as_deref());
        let reply = match &result {
            Ok(v) => wire::answer_line(id, op.unwrap_or_default(), elapsed_ms(start), v.clone()),
            Err(e) => wire::error_line(id, op, e),
        };
        if let Err(e) = write_line(out, &reply).await {
            return Outcome::failed(None, None, &e);
        }
        if let Err(e) = &result {
            // A dropped link, or an operation abandoned halfway, which leaves the connection's state unknown:
            // end, and let the node start another.
            if e.link_lost() || e.code == Code::Timeout {
                let mut ev = Map::new();
                ev.insert("reason".into(), json!(e.message));
                let _ = write_line(out, &wire::event_line("disconnected", ev)).await;
                return Outcome {
                    code: 1,
                    stderr_error: None,
                };
            }
        }
    }
}

async fn sleep_until(at: Option<Instant>) {
    match at {
        Some(at) => tokio::time::sleep_until(at).await,
        None => std::future::pending().await,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;
    use std::rc::Rc;

    #[derive(Default)]
    struct Log {
        ran: Vec<String>,
        idled: u32,
        closed: bool,
    }

    struct Fake {
        log: Rc<RefCell<Log>>,
        /// Errors to answer with, by op.
        fail: Vec<(&'static str, BridgeError)>,
        hold: Option<Instant>,
        slow: Duration,
    }

    impl Fake {
        fn new(log: &Rc<RefCell<Log>>) -> Self {
            Self {
                log: log.clone(),
                fail: Vec::new(),
                hold: None,
                slow: Duration::ZERO,
            }
        }
    }

    impl Phone for Fake {
        async fn run(&mut self, action: Action) -> Result<Value, BridgeError> {
            let name = format!("{action:?}");
            let op = name.split([' ', '(', '{']).next().unwrap().to_owned();
            self.log.borrow_mut().ran.push(op.clone());
            tokio::time::sleep(self.slow).await;
            if let Some((_, e)) = self.fail.iter().find(|(o, _)| *o == op) {
                return Err(e.clone());
            }
            Ok(json!({"did": op}))
        }
        fn idle_at(&self) -> Option<Instant> {
            self.hold
        }
        async fn idle(&mut self) {
            self.log.borrow_mut().idled += 1;
            self.hold = None;
        }
        async fn close(&mut self) {
            self.log.borrow_mut().closed = true;
        }
    }

    fn lines(out: &[u8]) -> Vec<Value> {
        std::str::from_utf8(out)
            .unwrap()
            .lines()
            .map(|l| serde_json::from_str(l).unwrap())
            .collect()
    }

    const DEVICE: &str = r#""device":{"udid":"U","address":"192.0.2.1","pairing_file":"/p"}"#;

    #[tokio::test]
    async fn call_answers_one_request() {
        let log = Rc::new(RefCell::new(Log::default()));
        let req = format!(r#"{{"v":1,"id":"a","op":"tap",{DEVICE},"args":{{"x":1,"y":2}}}}"#);
        let mut out = Vec::new();
        let l = log.clone();
        let o = call(&mut req.as_bytes(), &mut out, async move |spec: DeviceSpec| {
            assert_eq!(spec.udid, "U");
            Ok(Fake::new(&l))
        })
        .await;
        assert_eq!(o, Outcome::ok());
        let got = lines(&out);
        assert_eq!(got.len(), 1);
        assert_eq!(got[0]["id"], "a");
        assert_eq!(got[0]["ok"], true);
        assert_eq!(got[0]["result"]["did"], "Tap");
        assert!(log.borrow().closed);
    }

    #[tokio::test]
    async fn call_failures_go_to_stderr_with_nothing_on_stdout() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut out = Vec::new();
        let req = format!(r#"{{"v":1,"id":"b","op":"tap",{DEVICE},"args":{{"x":1}}}}"#);
        let l = log.clone();
        let o = call(&mut req.as_bytes(), &mut out, async move |_| Ok(Fake::new(&l))).await;
        assert_eq!(o.code, 1);
        let e: Value = serde_json::from_str(o.stderr_error.as_deref().unwrap()).unwrap();
        assert_eq!(e["error"]["code"], "bad_request");
        assert_eq!(e["id"], "b");
        assert!(out.is_empty());
        assert!(log.borrow().ran.is_empty(), "a bad request never reaches the phone");

        let req = format!(r#"{{"v":1,"op":"screenshot",{DEVICE}}}"#);
        let o = call(&mut req.as_bytes(), &mut out, async |_| {
            Err::<Fake, _>(BridgeError::new(Code::Unreachable, "no route"))
        })
        .await;
        let e: Value = serde_json::from_str(o.stderr_error.as_deref().unwrap()).unwrap();
        assert_eq!(e["error"]["code"], "unreachable");
        assert!(out.is_empty());

        let o = call(&mut r#"{"v":1,"op":"screenshot"}"#.as_bytes(), &mut out, async |_| {
            Ok(Fake::new(&Rc::new(RefCell::new(Log::default()))))
        })
        .await;
        assert!(o.stderr_error.unwrap().contains("device: required"));
        let o = call(&mut "".as_bytes(), &mut out, async |_| Ok(Fake::new(&log))).await;
        assert_eq!(o.code, 1);
    }

    #[tokio::test(start_paused = true)]
    async fn call_has_one_deadline_for_everything() {
        let log = Rc::new(RefCell::new(Log::default()));
        let req = format!(r#"{{"v":1,"op":"screenshot","timeout_ms":1000,{DEVICE}}}"#);
        let mut out = Vec::new();
        let l = log.clone();
        let o = call(&mut req.as_bytes(), &mut out, async move |_| {
            tokio::time::sleep(Duration::from_millis(600)).await;
            let mut f = Fake::new(&l);
            f.slow = Duration::from_millis(600);
            Ok(f)
        })
        .await;
        assert!(o.stderr_error.unwrap().contains(r#""code":"timeout""#));
    }

    #[tokio::test]
    async fn serve_answers_in_order_and_ends_at_eof() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut phone = Fake::new(&log);
        let input = concat!(
            r#"{"id":"1","op":"screenshot"}"#,
            "\n\n",
            r#"{"op":"tap","args":{"x":1,"y":1}}"#,
            "\n",
            r#"{"id":"3","op":"tap","args":{"x":1,"y":1}}"#,
            "\n",
        );
        let mut out = Vec::new();
        let o = serve(&mut phone, json!({"udid": "U"}), &mut input.as_bytes(), &mut out).await;
        assert_eq!(o, Outcome::ok());
        let got = lines(&out);
        assert_eq!(got[0], json!({"event": "ready", "device": {"udid": "U"}}));
        assert_eq!(got[1]["id"], "1");
        assert_eq!(got[1]["ok"], true);
        assert_eq!(got[2]["ok"], false, "serve needs an id");
        assert_eq!(got[2]["error"]["code"], "bad_request");
        assert_eq!(got[3]["id"], "3");
        assert_eq!(got.len(), 4);
        assert_eq!(log.borrow().ran, ["Screenshot", "Tap"]);
        assert!(log.borrow().closed);
    }

    #[tokio::test]
    async fn serve_drops_a_line_cut_off_by_eof() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut phone = Fake::new(&log);
        let input = concat!(
            r#"{"id":"1","op":"list_apps"}"#,
            "\n",
            r#"{"id":"2","op":"tap","args":{"x":1,"y":1}}"#
        );
        let mut out = Vec::new();
        let o = serve(&mut phone, json!({}), &mut input.as_bytes(), &mut out).await;
        assert_eq!(o.code, 0);
        assert_eq!(lines(&out).len(), 2);
        assert_eq!(log.borrow().ran, ["ListApps"]);
    }

    #[tokio::test]
    async fn call_takes_a_last_line_without_newline() {
        let log = Rc::new(RefCell::new(Log::default()));
        let req = format!(r#"{{"v":1,"op":"list_apps",{DEVICE}}}"#);
        let mut out = Vec::new();
        let l = log.clone();
        let o = call(&mut req.as_bytes(), &mut out, async move |_| Ok(Fake::new(&l))).await;
        assert_eq!(o.code, 0);
    }

    #[tokio::test]
    async fn serve_keeps_going_after_an_ordinary_error() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut phone = Fake::new(&log);
        phone
            .fail
            .push(("LaunchApp", BridgeError::new(Code::AppNotFound, "no such app")));
        let input = concat!(
            r#"{"id":"1","op":"launch_app","args":{"bundle_id":"com.example.none"}}"#,
            "\n",
            r#"{"id":"2","op":"list_apps"}"#,
            "\n"
        );
        let mut out = Vec::new();
        let o = serve(&mut phone, json!({}), &mut input.as_bytes(), &mut out).await;
        assert_eq!(o.code, 0);
        let got = lines(&out);
        assert_eq!(got[1]["error"]["code"], "app_not_found");
        assert_eq!(got[2]["ok"], true);
    }

    #[tokio::test]
    async fn serve_stops_when_the_link_drops() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut phone = Fake::new(&log);
        phone
            .fail
            .push(("Tap", BridgeError::new(Code::Disconnected, "tunnel closed")));
        let input = concat!(
            r#"{"id":"1","op":"tap","args":{"x":1,"y":1}}"#,
            "\n",
            r#"{"id":"2","op":"list_apps"}"#,
            "\n"
        );
        let mut out = Vec::new();
        let o = serve(&mut phone, json!({}), &mut input.as_bytes(), &mut out).await;
        assert_eq!(o.code, 1);
        let got = lines(&out);
        assert_eq!(got[1]["error"]["code"], "disconnected");
        assert_eq!(got[2], json!({"event": "disconnected", "reason": "tunnel closed"}));
        assert_eq!(got.len(), 3, "nothing after the link dropped");
        assert_eq!(log.borrow().ran, ["Tap"]);
    }

    #[tokio::test(start_paused = true)]
    async fn serve_stops_after_a_timeout() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut phone = Fake::new(&log);
        phone.slow = Duration::from_secs(5);
        let input = concat!(r#"{"id":"1","op":"screenshot","timeout_ms":1000}"#, "\n");
        let mut out = Vec::new();
        let o = serve(&mut phone, json!({}), &mut input.as_bytes(), &mut out).await;
        assert_eq!(o.code, 1);
        let got = lines(&out);
        assert_eq!(got[1]["error"]["code"], "timeout");
        assert_eq!(got[2]["event"], "disconnected");
    }

    #[tokio::test(start_paused = true)]
    async fn a_line_split_across_the_idle_deadline_survives() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut phone = Fake::new(&log);
        phone.hold = Some(Instant::now() + Duration::from_secs(10));
        let (mut writer, reader) = tokio::io::duplex(1024);
        let mut reader = tokio::io::BufReader::new(reader);
        let mut out = Vec::new();
        let feed = async move {
            writer.write_all(br#"{"id":"1","op":"#).await.unwrap();
            tokio::time::sleep(Duration::from_secs(15)).await;
            writer.write_all(b"\"list_apps\"}\n").await.unwrap();
            tokio::time::sleep(Duration::from_secs(1)).await;
            drop(writer.shutdown().await);
        };
        let (o, ()) = tokio::join!(serve(&mut phone, json!({}), &mut reader, &mut out), feed);
        assert_eq!(o.code, 0);
        assert_eq!(log.borrow().idled, 1);
        let got = lines(&out);
        assert_eq!(got[1]["id"], "1");
        assert_eq!(got[1]["ok"], true);
    }

    #[tokio::test(start_paused = true)]
    async fn serve_lets_go_when_idle() {
        let log = Rc::new(RefCell::new(Log::default()));
        let mut phone = Fake::new(&log);
        phone.hold = Some(Instant::now() + Duration::from_secs(10));
        let (mut writer, reader) = tokio::io::duplex(1024);
        let mut reader = tokio::io::BufReader::new(reader);
        let mut out = Vec::new();
        let feed = async move {
            tokio::time::sleep(Duration::from_secs(30)).await;
            drop(writer.shutdown().await);
        };
        let (o, ()) = tokio::join!(serve(&mut phone, json!({}), &mut reader, &mut out), feed);
        assert_eq!(o.code, 0);
        assert_eq!(log.borrow().idled, 1);
    }
}
