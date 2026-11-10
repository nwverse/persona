//! Each operation's arguments, checked before anything reaches the phone.

use serde_json::{Map, Value};

use crate::error::BridgeError;
use crate::geometry::{Direction, Point};
use crate::keys::{self, Button, Stroke};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ImageFormat {
    Jpeg,
    Png,
}

#[derive(Debug, Clone, PartialEq)]
pub struct ShotOptions {
    pub format: ImageFormat,
    pub quality: u8,
    pub max_dimension: u32,
    pub out_file: Option<String>,
}

#[derive(Debug, Clone, PartialEq)]
pub enum SwipeSpec {
    Ends(Point, Point),
    Direction {
        dir: Direction,
        origin: Option<Point>,
        distance: Option<f64>,
    },
}

#[derive(Debug, Clone, PartialEq)]
pub enum KeyInput {
    Text(Vec<Stroke>),
    Key { usage: u64, repeat: u32 },
}

#[derive(Debug, Clone, PartialEq)]
pub enum AppTarget {
    BundleId(String),
    Pid(u32),
}

#[derive(Debug, Clone, PartialEq)]
pub enum Action {
    DeviceInfo,
    Screenshot(ShotOptions),
    AxTree,
    Tap {
        at: Point,
        count: u32,
    },
    LongPress {
        at: Point,
        duration_ms: u64,
    },
    Drag {
        from: Point,
        to: Point,
        duration_ms: u64,
        hold_ms: u64,
    },
    Swipe {
        spec: SwipeSpec,
        duration_ms: u64,
    },
    Keys(KeyInput),
    Button {
        button: Button,
        hold_ms: u64,
    },
    LaunchApp {
        bundle_id: String,
        args: Vec<String>,
        env: Vec<(String, String)>,
        kill_existing: bool,
    },
    TerminateApp(AppTarget),
    ListApps {
        include_system: bool,
    },
}

impl Action {
    /// Whether it touches, types or presses: such operations go through the phone's input gate.
    pub fn is_input(&self) -> bool {
        matches!(
            self,
            Action::Tap { .. }
                | Action::LongPress { .. }
                | Action::Drag { .. }
                | Action::Swipe { .. }
                | Action::Keys(_)
                | Action::Button { .. }
        )
    }
}

/// Longest press, drag or swipe accepted.
const MAX_GESTURE_MS: u64 = 10_000;
const MAX_TAPS: u64 = 3;
const MAX_REPEAT: u64 = 100;
const MAX_TEXT: usize = 4_096;

pub fn parse(op: &str, args: &Map<String, Value>) -> Result<Action, BridgeError> {
    let a = Args(args);
    Ok(match op {
        "device_info" => Action::DeviceInfo,
        "screenshot" => Action::Screenshot(ShotOptions {
            format: match a.str_opt("format")?.unwrap_or("jpeg") {
                "jpeg" => ImageFormat::Jpeg,
                "png" => ImageFormat::Png,
                other => {
                    return Err(BridgeError::bad_request(format!(
                        "format: {other:?} is not jpeg or png"
                    )));
                }
            },
            quality: a.uint_in("quality", 85, 1, 100)? as u8,
            max_dimension: a.uint_in("max_dimension", 0, 0, 16_384)? as u32,
            out_file: a.str_opt("out_file")?.map(str::to_owned),
        }),
        "ax_tree" => Action::AxTree,
        "tap" => Action::Tap {
            at: a.point_here()?,
            count: a.uint_in("count", 1, 1, MAX_TAPS)? as u32,
        },
        "long_press" => Action::LongPress {
            at: a.point_here()?,
            duration_ms: a.uint_in("duration_ms", 800, 1, MAX_GESTURE_MS)?,
        },
        "drag" => Action::Drag {
            from: a.point("from")?,
            to: a.point("to")?,
            duration_ms: a.uint_in("duration_ms", 500, 1, MAX_GESTURE_MS)?,
            hold_ms: a.uint_in("hold_ms", 0, 0, MAX_GESTURE_MS)?,
        },
        "swipe" => {
            let spec = match (
                a.present("from").is_some() || a.present("to").is_some(),
                a.str_opt("direction")?,
            ) {
                (true, None) => SwipeSpec::Ends(a.point("from")?, a.point("to")?),
                (false, Some(d)) => SwipeSpec::Direction {
                    dir: Direction::parse(d).ok_or_else(|| {
                        BridgeError::bad_request(format!("direction: {d:?} is not up, down, left or right"))
                    })?,
                    origin: a.point_opt("origin")?,
                    distance: a.positive_opt("distance")?,
                },
                _ => return Err(BridgeError::bad_request("swipe takes either from and to, or direction")),
            };
            Action::Swipe {
                spec,
                duration_ms: a.uint_in("duration_ms", 250, 1, MAX_GESTURE_MS)?,
            }
        }
        "keys" => Action::Keys(match (a.str_opt("text")?, a.str_opt("key")?) {
            (Some(text), None) => {
                if text.is_empty() || text.chars().count() > MAX_TEXT {
                    return Err(BridgeError::bad_request(format!("text: 1 to {MAX_TEXT} characters")));
                }
                KeyInput::Text(keys::strokes(text)?)
            }
            (None, Some(name)) => KeyInput::Key {
                usage: keys::named_key(name)
                    .ok_or_else(|| BridgeError::bad_request(format!("key: unknown key {name:?}")))?,
                repeat: a.uint_in("repeat", 1, 1, MAX_REPEAT)? as u32,
            },
            _ => return Err(BridgeError::bad_request("keys takes exactly one of text and key")),
        }),
        "button" => {
            let name = a.str_req("name")?;
            let button =
                keys::button(name).ok_or_else(|| BridgeError::bad_request(format!("name: unknown button {name:?}")))?;
            let hold = a.uint_in("duration_ms", 0, 0, MAX_GESTURE_MS)?;
            Action::Button {
                button,
                hold_ms: if hold == 0 { button.press_ms } else { hold },
            }
        }
        "launch_app" => Action::LaunchApp {
            bundle_id: a.bundle_id()?,
            args: match args.get("args") {
                None | Some(Value::Null) => Vec::new(),
                Some(Value::Array(items)) => items
                    .iter()
                    .map(|v| v.as_str().map(str::to_owned))
                    .collect::<Option<_>>()
                    .ok_or_else(|| BridgeError::bad_request("args: an array of strings"))?,
                Some(_) => return Err(BridgeError::bad_request("args: an array of strings")),
            },
            env: match args.get("env") {
                None | Some(Value::Null) => Vec::new(),
                Some(Value::Object(m)) => m
                    .iter()
                    .map(|(k, v)| v.as_str().map(|v| (k.clone(), v.to_owned())))
                    .collect::<Option<_>>()
                    .ok_or_else(|| BridgeError::bad_request("env: an object of strings"))?,
                Some(_) => return Err(BridgeError::bad_request("env: an object of strings")),
            },
            kill_existing: a.bool_opt("kill_existing")?.unwrap_or(false),
        },
        "terminate_app" => Action::TerminateApp(match (a.present("bundle_id").is_some(), a.present("pid")) {
            (true, None) => AppTarget::BundleId(a.bundle_id()?),
            (false, Some(_)) => AppTarget::Pid(a.uint_in("pid", 0, 1, u64::from(u32::MAX))? as u32),
            _ => {
                return Err(BridgeError::bad_request(
                    "terminate_app takes exactly one of bundle_id and pid",
                ));
            }
        }),
        "list_apps" => Action::ListApps {
            include_system: a.bool_opt("include_system")?.unwrap_or(true),
        },
        other => return Err(BridgeError::bad_request(format!("unknown op {other:?}"))),
    })
}

struct Args<'a>(&'a Map<String, Value>);

impl Args<'_> {
    fn present(&self, key: &str) -> Option<&Value> {
        self.0.get(key).filter(|v| !v.is_null())
    }

    fn str_opt(&self, key: &str) -> Result<Option<&str>, BridgeError> {
        match self.present(key) {
            None => Ok(None),
            Some(Value::String(s)) => Ok(Some(s)),
            Some(_) => Err(BridgeError::bad_request(format!("{key}: a string"))),
        }
    }

    fn str_req(&self, key: &str) -> Result<&str, BridgeError> {
        self.str_opt(key)?
            .ok_or_else(|| BridgeError::bad_request(format!("{key}: required")))
    }

    fn bool_opt(&self, key: &str) -> Result<Option<bool>, BridgeError> {
        match self.present(key) {
            None => Ok(None),
            Some(Value::Bool(b)) => Ok(Some(*b)),
            Some(_) => Err(BridgeError::bad_request(format!("{key}: true or false"))),
        }
    }

    fn uint_in(&self, key: &str, default: u64, min: u64, max: u64) -> Result<u64, BridgeError> {
        let n = match self.present(key) {
            None => return Ok(default),
            Some(v) => v.as_u64(),
        };
        match n {
            Some(n) if (min..=max).contains(&n) => Ok(n),
            _ => Err(BridgeError::bad_request(format!(
                "{key}: an integer from {min} to {max}"
            ))),
        }
    }

    fn positive_opt(&self, key: &str) -> Result<Option<f64>, BridgeError> {
        match self.present(key) {
            None => Ok(None),
            Some(v) => match v.as_f64() {
                Some(f) if f.is_finite() && f > 0.0 => Ok(Some(f)),
                _ => Err(BridgeError::bad_request(format!("{key}: a positive number"))),
            },
        }
    }

    fn coord(v: Option<&Value>, what: &str) -> Result<f64, BridgeError> {
        match v.and_then(Value::as_f64) {
            Some(f) if f.is_finite() => Ok(f),
            _ => Err(BridgeError::bad_request(format!("{what}: a number of points"))),
        }
    }

    /// `x` and `y` of the arguments themselves.
    fn point_here(&self) -> Result<Point, BridgeError> {
        Ok(Point {
            x: Self::coord(self.present("x"), "x")?,
            y: Self::coord(self.present("y"), "y")?,
        })
    }

    fn point_opt(&self, key: &str) -> Result<Option<Point>, BridgeError> {
        match self.present(key) {
            None => Ok(None),
            Some(Value::Object(m)) => Ok(Some(Point {
                x: Self::coord(m.get("x"), &format!("{key}.x"))?,
                y: Self::coord(m.get("y"), &format!("{key}.y"))?,
            })),
            Some(_) => Err(BridgeError::bad_request(format!("{key}: an object with x and y"))),
        }
    }

    fn point(&self, key: &str) -> Result<Point, BridgeError> {
        self.point_opt(key)?
            .ok_or_else(|| BridgeError::bad_request(format!("{key}: required")))
    }

    fn bundle_id(&self) -> Result<String, BridgeError> {
        let id = self.str_req("bundle_id")?;
        let ok = !id.is_empty()
            && id.len() <= 255
            && id
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '_'));
        if !ok {
            return Err(BridgeError::bad_request(format!(
                "bundle_id: {id:?} is not a bundle ID"
            )));
        }
        Ok(id.to_owned())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::error::Code;
    use serde_json::json;

    fn args(v: Value) -> Map<String, Value> {
        v.as_object().unwrap().clone()
    }

    fn p(x: f64, y: f64) -> Point {
        Point { x, y }
    }

    #[test]
    fn screenshot_defaults() {
        let a = parse("screenshot", &Map::new()).unwrap();
        assert_eq!(
            a,
            Action::Screenshot(ShotOptions {
                format: ImageFormat::Jpeg,
                quality: 85,
                max_dimension: 0,
                out_file: None
            })
        );
        assert!(parse("screenshot", &args(json!({"format": "gif"}))).is_err());
        assert!(parse("screenshot", &args(json!({"quality": 0}))).is_err());
    }

    #[test]
    fn touches() {
        assert_eq!(
            parse("tap", &args(json!({"x": 1.5, "y": 2}))).unwrap(),
            Action::Tap {
                at: p(1.5, 2.0),
                count: 1
            }
        );
        assert!(parse("tap", &args(json!({"x": 1}))).is_err());
        assert!(parse("tap", &args(json!({"x": "1", "y": 2}))).is_err());
        assert!(parse("tap", &args(json!({"x": 1, "y": 2, "count": 9}))).is_err());
        assert_eq!(
            parse("long_press", &args(json!({"x": 1, "y": 2}))).unwrap(),
            Action::LongPress {
                at: p(1.0, 2.0),
                duration_ms: 800
            }
        );
        assert_eq!(
            parse(
                "drag",
                &args(json!({"from": {"x": 0, "y": 0}, "to": {"x": 5, "y": 5}, "hold_ms": 300}))
            )
            .unwrap(),
            Action::Drag {
                from: p(0.0, 0.0),
                to: p(5.0, 5.0),
                duration_ms: 500,
                hold_ms: 300
            }
        );
        assert!(parse("drag", &args(json!({"from": {"x": 0, "y": 0}}))).is_err());
        assert!(parse("long_press", &args(json!({"x": 1, "y": 2, "duration_ms": 999_999}))).is_err());
    }

    #[test]
    fn swipes_take_ends_or_a_direction() {
        assert_eq!(
            parse("swipe", &args(json!({"direction": "up"}))).unwrap(),
            Action::Swipe {
                spec: SwipeSpec::Direction {
                    dir: Direction::Up,
                    origin: None,
                    distance: None
                },
                duration_ms: 250
            }
        );
        assert!(matches!(
            parse(
                "swipe",
                &args(json!({"from": {"x": 0, "y": 0}, "to": {"x": 0, "y": 9}}))
            )
            .unwrap(),
            Action::Swipe {
                spec: SwipeSpec::Ends(_, _),
                ..
            }
        ));
        assert!(parse("swipe", &Map::new()).is_err());
        assert!(matches!(
            parse("swipe", &args(json!({"direction": "up", "from": null}))).unwrap(),
            Action::Swipe {
                spec: SwipeSpec::Direction { .. },
                ..
            }
        ));
        assert!(parse("swipe", &args(json!({"direction": "up", "from": {"x": 0, "y": 0}}))).is_err());
        assert!(parse("swipe", &args(json!({"direction": "up", "distance": -3}))).is_err());
    }

    #[test]
    fn keys_take_text_or_one_key() {
        assert!(
            matches!(parse("keys", &args(json!({"text": "ok"}))).unwrap(), Action::Keys(KeyInput::Text(s)) if s.len() == 2)
        );
        assert_eq!(
            parse("keys", &args(json!({"key": "backspace", "repeat": 3}))).unwrap(),
            Action::Keys(KeyInput::Key { usage: 0x2A, repeat: 3 })
        );
        assert!(parse("keys", &args(json!({"text": "a", "key": "return"}))).is_err());
        assert!(parse("keys", &Map::new()).is_err());
        assert!(parse("keys", &args(json!({"text": ""}))).is_err());
        assert_eq!(
            parse("keys", &args(json!({"text": "日本"}))).unwrap_err().code,
            Code::Unsupported
        );
    }

    #[test]
    fn buttons_hold_for_their_default() {
        let Action::Button { hold_ms, .. } = parse("button", &args(json!({"name": "lock"}))).unwrap() else {
            panic!()
        };
        assert_eq!(hold_ms, 200);
        let Action::Button { hold_ms, .. } =
            parse("button", &args(json!({"name": "lock", "duration_ms": 3000}))).unwrap()
        else {
            panic!()
        };
        assert_eq!(hold_ms, 3000);
        assert!(parse("button", &args(json!({"name": "eject"}))).is_err());
    }

    #[test]
    fn apps() {
        assert_eq!(
            parse(
                "launch_app",
                &args(json!({"bundle_id": "com.example.App", "args": ["-x"], "env": {"A": "1"}}))
            )
            .unwrap(),
            Action::LaunchApp {
                bundle_id: "com.example.App".into(),
                args: vec!["-x".into()],
                env: vec![("A".into(), "1".into())],
                kill_existing: false
            }
        );
        assert!(parse("launch_app", &args(json!({"bundle_id": "com.example/../x"}))).is_err());
        assert!(parse("launch_app", &args(json!({"bundle_id": "a", "args": [1]}))).is_err());
        assert_eq!(
            parse("terminate_app", &args(json!({"pid": 42}))).unwrap(),
            Action::TerminateApp(AppTarget::Pid(42))
        );
        assert!(parse("terminate_app", &args(json!({"pid": 42, "bundle_id": "a"}))).is_err());
        assert!(parse("terminate_app", &Map::new()).is_err());
        assert!(parse("terminate_app", &args(json!({"pid": null}))).is_err());
        assert_eq!(
            parse("terminate_app", &args(json!({"bundle_id": "a", "pid": null}))).unwrap(),
            Action::TerminateApp(AppTarget::BundleId("a".into()))
        );
        assert_eq!(
            parse("list_apps", &Map::new()).unwrap(),
            Action::ListApps { include_system: true }
        );
    }

    #[test]
    fn input_operations_are_marked() {
        assert!(parse("tap", &args(json!({"x": 1, "y": 1}))).unwrap().is_input());
        assert!(!parse("screenshot", &Map::new()).unwrap().is_input());
        assert!(
            !parse("launch_app", &args(json!({"bundle_id": "a"})))
                .unwrap()
                .is_input()
        );
    }
}
