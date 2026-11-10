//! The simulator backend, for development and testing: the same operations on an iOS simulator on this
//! computer, through `xcrun simctl`, so everything above the bridge can run end to end without a phone.
//!
//! It never boots, shuts down or erases a simulator: booting is `scripts/app/sim-boot.sh`'s job. Touches, keys
//! and the accessibility tree answer `unsupported` until the test runner that provides them exists.

use std::path::Path;

use serde_json::{Value, json};

use crate::error::{BridgeError, Code};
use crate::geometry::Screen;
use crate::ops::{Action, AppTarget, ShotOptions};
use crate::record::SimRecord;
use crate::run::Phone;
use crate::shot;

/// What one command printed.
#[derive(Debug, Clone, Default)]
pub struct Output {
    pub ok: bool,
    pub stdout: Vec<u8>,
    pub stderr: String,
}

/// Runs `xcrun` and `plutil`; tests use a stand-in.
#[allow(async_fn_in_trait)]
pub trait Runner {
    async fn run(&self, program: &str, args: &[String], stdin: Option<&[u8]>, env: &[(String, String)]) -> Output;
}

/// The real commands, killed if the request they serve is abandoned.
pub struct System;

impl Runner for System {
    async fn run(&self, program: &str, args: &[String], stdin: Option<&[u8]>, env: &[(String, String)]) -> Output {
        use tokio::io::AsyncWriteExt as _;
        let mut cmd = tokio::process::Command::new(program);
        cmd.args(args)
            .envs(env.iter().map(|(k, v)| (k, v)))
            .stdin(if stdin.is_some() {
                std::process::Stdio::piped()
            } else {
                std::process::Stdio::null()
            })
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .kill_on_drop(true);
        let mut child = match cmd.spawn() {
            Ok(c) => c,
            Err(e) => {
                return Output {
                    ok: false,
                    stdout: Vec::new(),
                    stderr: format!("run {program}: {e}"),
                };
            }
        };
        if let (Some(input), Some(mut pipe)) = (stdin, child.stdin.take()) {
            let _ = pipe.write_all(input).await;
        }
        match child.wait_with_output().await {
            Ok(out) => Output {
                ok: out.status.success(),
                stdout: out.stdout,
                stderr: String::from_utf8_lossy(&out.stderr).into_owned(),
            },
            Err(e) => Output {
                ok: false,
                stdout: Vec::new(),
                stderr: format!("run {program}: {e}"),
            },
        }
    }
}

fn strings(args: &[&str]) -> Vec<String> {
    args.iter().map(|s| (*s).to_owned()).collect()
}

/// The last non-empty line of a command's complaint, for messages.
fn complaint(o: &Output) -> String {
    o.stderr
        .lines()
        .map(str::trim)
        .rfind(|l| !l.is_empty())
        .unwrap_or("no output")
        .to_owned()
}

/// A simulator as `simctl list devices -j` describes it.
#[derive(Debug, Clone, PartialEq)]
pub struct SimInfo {
    pub udid: String,
    pub name: String,
    pub state: String,
    /// "27.2" from the runtime "com.apple.CoreSimulator.SimRuntime.iOS-27-2".
    pub os_version: Option<String>,
    pub device_type: String,
}

async fn find<R: Runner>(runner: &R, udid: &str) -> Result<Option<SimInfo>, BridgeError> {
    let o = runner
        .run("xcrun", &strings(&["simctl", "list", "devices", "-j"]), None, &[])
        .await;
    if !o.ok {
        return Err(BridgeError::new(
            Code::ServiceUnavailable,
            format!("simctl list: {}", complaint(&o)),
        ));
    }
    let v: Value = serde_json::from_slice(&o.stdout)
        .map_err(|e| BridgeError::internal(format!("simctl list: unreadable answer: {e}")))?;
    Ok(parse_devices(&v)
        .into_iter()
        .find(|d| d.udid.eq_ignore_ascii_case(udid)))
}

fn parse_devices(v: &Value) -> Vec<SimInfo> {
    let Some(runtimes) = v.get("devices").and_then(Value::as_object) else {
        return Vec::new();
    };
    let mut out = Vec::new();
    for (runtime, devices) in runtimes {
        let os_version = runtime
            .rsplit('.')
            .next()
            .and_then(|r| r.split_once('-'))
            .map(|(_, v)| v.replace('-', "."));
        for d in devices.as_array().into_iter().flatten() {
            let text = |k: &str| d.get(k).and_then(Value::as_str).unwrap_or_default().to_owned();
            out.push(SimInfo {
                udid: text("udid"),
                name: text("name"),
                state: text("state"),
                os_version: os_version.clone(),
                device_type: text("deviceTypeIdentifier"),
            });
        }
    }
    out
}

/// Whether `udid` is one of this computer's simulators: `pair` then makes a simulator record.
pub async fn is_simulator<R: Runner>(runner: &R, udid: &str) -> bool {
    matches!(find(runner, udid).await, Ok(Some(_)))
}

/// `pair` for a simulator: writes a record naming it. No USB or trust involved.
pub async fn pair<R: Runner>(runner: &R, udid: &str, out: &Path) -> Result<Value, BridgeError> {
    let info = find(runner, udid)
        .await?
        .ok_or_else(|| BridgeError::new(Code::NotFound, format!("no simulator {udid} on this computer")))?;
    SimRecord {
        udid: info.udid.clone(),
    }
    .write(out)?;
    let model = getenv(runner, &info.udid, "SIMULATOR_MODEL_IDENTIFIER").await;
    Ok(json!({
        "udid": info.udid,
        "name": info.name,
        "model": model,
        "product_version": info.os_version,
        "transport": "simulator",
    }))
}

async fn getenv<R: Runner>(runner: &R, udid: &str, name: &str) -> Option<String> {
    let o = runner
        .run("xcrun", &strings(&["simctl", "getenv", udid, name]), None, &[])
        .await;
    let v = String::from_utf8_lossy(&o.stdout).trim().to_owned();
    (o.ok && !v.is_empty()).then_some(v)
}

pub struct SimDevice<R: Runner> {
    runner: R,
    info: SimInfo,
    model: Option<String>,
    build: Option<String>,
    screen: Option<Screen>,
}

impl<R: Runner> SimDevice<R> {
    /// Opens the simulator a record names; it has to be booted already.
    pub async fn connect(runner: R, record: &SimRecord, udid: &str) -> Result<Self, BridgeError> {
        if !record.udid.eq_ignore_ascii_case(udid) {
            return Err(BridgeError::new(
                Code::WrongDevice,
                format!("the record is for simulator {}, not {udid}", record.udid),
            ));
        }
        let info = find(&runner, &record.udid).await?.ok_or_else(|| {
            BridgeError::new(
                Code::PairingRejected,
                format!(
                    "simulator {} no longer exists on this computer; pair another",
                    record.udid
                ),
            )
        })?;
        if info.state != "Booted" {
            return Err(BridgeError::new(
                Code::Unreachable,
                format!(
                    "simulator {} is {}; boot it with scripts/app/sim-boot.sh",
                    info.udid,
                    info.state.to_lowercase()
                ),
            ));
        }
        let env = |name| getenv(&runner, &info.udid, name);
        let model = env("SIMULATOR_MODEL_IDENTIFIER").await;
        let build = env("SIMULATOR_RUNTIME_BUILD_VERSION").await;
        let num = |v: Option<String>| v.and_then(|v| v.parse::<f64>().ok()).filter(|n| *n > 0.0);
        let w = num(env("SIMULATOR_MAINSCREEN_WIDTH").await);
        let h = num(env("SIMULATOR_MAINSCREEN_HEIGHT").await);
        let scale = num(env("SIMULATOR_MAINSCREEN_SCALE").await);
        let screen = match (w, h, scale) {
            (Some(w), Some(h), Some(scale)) => Some(Screen {
                pixel_width: w.min(h) as u32,
                pixel_height: w.max(h) as u32,
                scale,
            }),
            _ => None,
        };
        Ok(Self {
            runner,
            info,
            model,
            build,
            screen,
        })
    }

    async fn simctl(&self, args: &[&str]) -> Output {
        self.runner.run("xcrun", &strings(args), None, &[]).await
    }

    fn device_info(&self) -> Value {
        let frame = self.screen.map(|s| s.frame(crate::geometry::Orientation::Portrait));
        json!({
            "udid": self.info.udid,
            "name": self.info.name,
            "product_type": self.model,
            "os_version": self.info.os_version,
            "build": self.build,
            "point_width": frame.map(|f| f.0),
            "point_height": frame.map(|f| f.1),
            "scale": self.screen.map(|s| s.scale),
            "orientation": "portrait",
            "locked": Value::Null,
            "developer_mode": Value::Null,
            "ddi_mounted": Value::Null,
            "battery": Value::Null,
            "simulated": true,
            "transport": "simulator",
            "capabilities": {"screenshot": true, "ax_tree": false, "input": false, "apps": true},
            "notes": {
                "ax_tree": Code::Unsupported.as_str(),
                "input": Code::Unsupported.as_str(),
                "simulated": "an iOS simulator on this computer: the development and testing backend",
            },
        })
    }

    async fn screenshot(&self, opts: ShotOptions) -> Result<Value, BridgeError> {
        let dir = std::env::temp_dir().join(format!("persona-iphone-bridge-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir(&dir).map_err(|e| BridgeError::internal(format!("temporary directory: {e}")))?;
        let file = dir.join("screen.png");
        let o = self
            .simctl(&[
                "simctl",
                "io",
                &self.info.udid,
                "screenshot",
                "--type=png",
                &file.to_string_lossy(),
            ])
            .await;
        let raw = std::fs::read(&file);
        let _ = std::fs::remove_dir_all(&dir);
        if !o.ok {
            return Err(BridgeError::new(
                Code::ServiceUnavailable,
                format!("screenshot: {}", complaint(&o)),
            ));
        }
        let raw = raw.map_err(|e| BridgeError::internal(format!("screenshot: {e}")))?;
        // The simulator's orientation cannot be read without the test runner; a capture wider than tall is
        // reported as landscape, and is never turned.
        let image = shot::prepare(raw, &opts, crate::geometry::Orientation::Portrait)?;
        let scale = self.screen.map_or(3.0, |s| s.scale);
        let landscape = image.width > image.height;
        let (pw, ph) = match self.screen {
            Some(s) => s.frame(if landscape {
                crate::geometry::Orientation::LandscapeRight
            } else {
                crate::geometry::Orientation::Portrait
            }),
            None => (f64::from(image.width) / scale, f64::from(image.height) / scale),
        };
        let mut out = json!({
            "mime": image.mime,
            "width": image.width,
            "height": image.height,
            "point_width": pw,
            "point_height": ph,
            "orientation": if landscape { "landscape_right" } else { "portrait" },
        });
        match &opts.out_file {
            Some(path) => crate::files::write_private(Path::new(path), &image.bytes)?,
            None => {
                use base64::Engine as _;
                out["data"] = json!(base64::engine::general_purpose::STANDARD.encode(&image.bytes));
            }
        }
        Ok(out)
    }

    /// Installed apps: `(bundle_id, name, version, system)`.
    async fn apps(&self) -> Result<Vec<App>, BridgeError> {
        let o = self.simctl(&["simctl", "listapps", &self.info.udid]).await;
        if !o.ok {
            return Err(BridgeError::new(
                Code::ServiceUnavailable,
                format!("listapps: {}", complaint(&o)),
            ));
        }
        // simctl prints an old-style (OpenStep) property list; plutil turns it into XML.
        let xml = self
            .runner
            .run(
                "plutil",
                &strings(&["-convert", "xml1", "-o", "-", "-"]),
                Some(&o.stdout),
                &[],
            )
            .await;
        if !xml.ok {
            return Err(BridgeError::internal(format!("listapps: {}", complaint(&xml))));
        }
        parse_apps(&xml.stdout)
    }

    async fn list_apps(&self, include_system: bool) -> Result<Value, BridgeError> {
        let apps: Vec<Value> = self
            .apps()
            .await?
            .into_iter()
            .filter(|a| include_system || !a.3)
            .map(|(bundle_id, name, version, system)| {
                json!({"bundle_id": bundle_id, "name": name, "version": version, "type": if system { "system" } else { "user" }})
            })
            .collect();
        Ok(json!({ "apps": apps }))
    }

    async fn installed(&self, bundle_id: &str) -> Result<bool, BridgeError> {
        Ok(self.apps().await?.iter().any(|a| a.0 == bundle_id))
    }

    async fn launch_app(
        &self,
        bundle_id: String,
        args: Vec<String>,
        env: Vec<(String, String)>,
        kill_existing: bool,
    ) -> Result<Value, BridgeError> {
        let mut cmd = strings(&["simctl", "launch"]);
        if kill_existing {
            cmd.push("--terminate-running-process".into());
        }
        cmd.push(self.info.udid.clone());
        cmd.push(bundle_id.clone());
        cmd.extend(args);
        // simctl hands SIMCTL_CHILD_ variables to the app without the prefix.
        let env: Vec<(String, String)> = env.into_iter().map(|(k, v)| (format!("SIMCTL_CHILD_{k}"), v)).collect();
        let o = self.runner.run("xcrun", &cmd, None, &env).await;
        if !o.ok {
            if !self.installed(&bundle_id).await.unwrap_or(true) {
                return Err(BridgeError::new(
                    Code::AppNotFound,
                    format!("no app {bundle_id} on the simulator"),
                ));
            }
            return Err(BridgeError::internal(format!("launch {bundle_id}: {}", complaint(&o))));
        }
        let pid = parse_launch(&String::from_utf8_lossy(&o.stdout), &bundle_id);
        Ok(json!({"bundle_id": bundle_id, "pid": pid}))
    }

    async fn terminate_app(&self, target: AppTarget) -> Result<Value, BridgeError> {
        let bundle_id = match target {
            AppTarget::BundleId(id) => id,
            AppTarget::Pid(pid) => {
                // Only an app process of this simulator, never anything else on the computer.
                let o = self
                    .simctl(&["simctl", "spawn", &self.info.udid, "launchctl", "list"])
                    .await;
                if !o.ok {
                    return Err(BridgeError::internal(format!("launchctl list: {}", complaint(&o))));
                }
                match app_of_pid(&String::from_utf8_lossy(&o.stdout), pid) {
                    Some(id) => id,
                    None => return Ok(json!({"terminated": false})),
                }
            }
        };
        let o = self.simctl(&["simctl", "terminate", &self.info.udid, &bundle_id]).await;
        if o.ok {
            return Ok(json!({"terminated": true}));
        }
        if !self.installed(&bundle_id).await.unwrap_or(true) {
            return Err(BridgeError::new(
                Code::AppNotFound,
                format!("no app {bundle_id} on the simulator"),
            ));
        }
        if o.stderr.contains("found nothing to terminate") {
            return Ok(json!({"terminated": false}));
        }
        Err(BridgeError::internal(format!(
            "terminate {bundle_id}: {}",
            complaint(&o)
        )))
    }
}

/// An installed app: bundle ID, name, version, and whether it is Apple's own.
type App = (String, String, Option<String>, bool);

fn parse_apps(xml: &[u8]) -> Result<Vec<App>, BridgeError> {
    let v: plist::Value =
        plist::from_bytes(xml).map_err(|e| BridgeError::internal(format!("listapps: unreadable answer: {e}")))?;
    let d = v
        .as_dictionary()
        .ok_or_else(|| BridgeError::internal("listapps: not a dictionary"))?;
    let mut apps: Vec<_> = d
        .iter()
        .filter_map(|(id, app)| {
            let app = app.as_dictionary()?;
            let text = |k: &str| app.get(k).and_then(plist::Value::as_string).map(str::to_owned);
            let name = text("CFBundleDisplayName")
                .or_else(|| text("CFBundleName"))
                .unwrap_or_else(|| id.clone());
            let system = text("ApplicationType").as_deref() == Some("System");
            Some((id.clone(), name, text("CFBundleShortVersionString"), system))
        })
        .collect();
    apps.sort();
    Ok(apps)
}

/// "com.example.App: 2047" → 2047.
fn parse_launch(out: &str, bundle_id: &str) -> Option<u32> {
    out.lines()
        .find_map(|l| l.trim().strip_prefix(bundle_id)?.strip_prefix(':')?.trim().parse().ok())
}

/// The app whose process is `pid`, from `launchctl list` inside the simulator
/// ("2047  0  UIKitApplication:com.apple.Preferences[9791][rb-legacy]").
fn app_of_pid(list: &str, pid: u32) -> Option<String> {
    list.lines().find_map(|l| {
        let mut f = l.split_whitespace();
        let p: u32 = f.next()?.parse().ok()?;
        let label = f.nth(1)?;
        let app = label.strip_prefix("UIKitApplication:")?;
        (p == pid).then(|| app.split('[').next().unwrap_or(app).to_owned())
    })
}

impl<R: Runner> Phone for SimDevice<R> {
    async fn run(&mut self, action: Action) -> Result<Value, BridgeError> {
        match action {
            Action::DeviceInfo => Ok(self.device_info()),
            Action::Screenshot(opts) => self.screenshot(opts).await,
            Action::ListApps { include_system } => self.list_apps(include_system).await,
            Action::LaunchApp {
                bundle_id,
                args,
                env,
                kill_existing,
            } => self.launch_app(bundle_id, args, env, kill_existing).await,
            Action::TerminateApp(t) => self.terminate_app(t).await,
            Action::AxTree => Err(BridgeError::new(
                Code::Unsupported,
                "the simulator backend has no accessibility tree yet; use screenshot",
            )),
            _ => Err(BridgeError::new(
                Code::Unsupported,
                "the simulator backend cannot touch, type or press buttons yet",
            )),
        }
    }

    fn idle_at(&self) -> Option<tokio::time::Instant> {
        None
    }

    async fn idle(&mut self) {}

    async fn close(&mut self) {}
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;

    const UDID: &str = "11111111-2222-3333-4444-555555555555";

    /// Answers by the command's first words; records what ran.
    #[derive(Default)]
    struct Fake {
        answers: Vec<(String, Output)>,
        ran: RefCell<Vec<String>>,
    }

    impl Fake {
        fn answer(mut self, prefix: impl Into<String>, ok: bool, stdout: &str, stderr: &str) -> Self {
            self.answers.push((
                prefix.into(),
                Output {
                    ok,
                    stdout: stdout.as_bytes().to_vec(),
                    stderr: stderr.into(),
                },
            ));
            self
        }
    }

    impl Runner for Fake {
        async fn run(&self, program: &str, args: &[String], stdin: Option<&[u8]>, env: &[(String, String)]) -> Output {
            let line = format!("{program} {}", args.join(" "));
            let mut logged = line.clone();
            for (k, v) in env {
                logged.push_str(&format!(" [{k}={v}]"));
            }
            self.ran.borrow_mut().push(logged);
            if program == "plutil" {
                // Stand in for the conversion: the fixtures are XML already.
                return Output {
                    ok: true,
                    stdout: stdin.unwrap_or_default().to_vec(),
                    stderr: String::new(),
                };
            }
            if let Some(path) = line.strip_prefix(&format!("xcrun simctl io {UDID} screenshot --type=png ")) {
                std::fs::write(path, png(30, 60)).unwrap();
                return Output {
                    ok: true,
                    ..Output::default()
                };
            }
            self.answers
                .iter()
                .find(|(p, _)| line.starts_with(p.as_str()))
                .map(|(_, o)| o.clone())
                .unwrap_or_default()
        }
    }

    fn png(w: u32, h: u32) -> Vec<u8> {
        let mut out = Vec::new();
        image::DynamicImage::new_rgb8(w, h)
            .write_to(&mut std::io::Cursor::new(&mut out), image::ImageFormat::Png)
            .unwrap();
        out
    }

    fn devices(state: &str) -> String {
        json!({"devices": {"com.apple.CoreSimulator.SimRuntime.iOS-27-2": [
            {"udid": UDID, "name": "Test Phone", "state": state, "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro"}
        ]}})
        .to_string()
    }

    const APPS: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>com.apple.Preferences</key><dict><key>ApplicationType</key><string>System</string><key>CFBundleDisplayName</key><string>Settings</string><key>CFBundleShortVersionString</key><string>1.0</string></dict>
<key>com.example.App</key><dict><key>ApplicationType</key><string>User</string><key>CFBundleName</key><string>Example</string><key>CFBundleShortVersionString</key><string>2.1</string></dict>
</dict></plist>"#;

    fn booted() -> Fake {
        Fake::default()
            .answer("xcrun simctl list devices", true, &devices("Booted"), "")
            .answer(
                format!("xcrun simctl getenv {UDID} SIMULATOR_MODEL_IDENTIFIER"),
                true,
                "iPhone18,1\n",
                "",
            )
            .answer(
                format!("xcrun simctl getenv {UDID} SIMULATOR_MAINSCREEN_WIDTH"),
                true,
                "1206\n",
                "",
            )
            .answer(
                format!("xcrun simctl getenv {UDID} SIMULATOR_MAINSCREEN_HEIGHT"),
                true,
                "2622\n",
                "",
            )
            .answer(
                format!("xcrun simctl getenv {UDID} SIMULATOR_MAINSCREEN_SCALE"),
                true,
                "3.000000\n",
                "",
            )
            .answer("xcrun simctl listapps", true, APPS, "")
    }

    async fn open(fake: Fake) -> SimDevice<Fake> {
        SimDevice::connect(fake, &SimRecord { udid: UDID.into() }, UDID)
            .await
            .unwrap()
    }

    #[tokio::test]
    async fn describes_itself_as_a_simulator() {
        let mut d = open(booted()).await;
        let info = d.run(Action::DeviceInfo).await.unwrap();
        assert_eq!(info["simulated"], true);
        assert_eq!(info["transport"], "simulator");
        assert_eq!(info["product_type"], "iPhone18,1");
        assert_eq!(info["os_version"], "27.2");
        assert_eq!(info["point_width"], 402.0);
        assert_eq!(info["point_height"], 874.0);
        assert_eq!(info["capabilities"]["input"], false);
        assert_eq!(info["notes"]["input"], "unsupported");
    }

    #[tokio::test]
    async fn needs_a_booted_simulator_and_never_boots_one() {
        let fake = Fake::default().answer("xcrun simctl list devices", true, &devices("Shutdown"), "");
        let err = SimDevice::connect(fake, &SimRecord { udid: UDID.into() }, UDID)
            .await
            .err()
            .unwrap();
        assert_eq!(err.code, Code::Unreachable);
        assert!(err.message.contains("sim-boot.sh"));

        let fake = Fake::default().answer("xcrun simctl list devices", true, r#"{"devices":{}}"#, "");
        let err = SimDevice::connect(fake, &SimRecord { udid: UDID.into() }, UDID)
            .await
            .err()
            .unwrap();
        assert_eq!(err.code, Code::PairingRejected);

        let err = SimDevice::connect(Fake::default(), &SimRecord { udid: UDID.into() }, "OTHER")
            .await
            .err()
            .unwrap();
        assert_eq!(err.code, Code::WrongDevice);
    }

    #[tokio::test]
    async fn takes_screenshots() {
        let mut d = open(booted()).await;
        let shot = d
            .run(Action::Screenshot(ShotOptions {
                format: crate::ops::ImageFormat::Png,
                quality: 85,
                max_dimension: 0,
                out_file: None,
            }))
            .await
            .unwrap();
        assert_eq!(shot["mime"], "image/png");
        assert_eq!((shot["width"].as_u64(), shot["height"].as_u64()), (Some(30), Some(60)));
        assert_eq!(shot["point_width"], 402.0);
        assert!(shot["data"].as_str().is_some_and(|d| !d.is_empty()));
    }

    #[tokio::test]
    async fn lists_launches_and_terminates_apps() {
        let fake = booted()
            .answer(
                format!("xcrun simctl launch {UDID} com.example.App"),
                true,
                "com.example.App: 2047\n",
                "",
            )
            .answer(
                format!("xcrun simctl spawn {UDID} launchctl list"),
                true,
                "PID\tStatus\tLabel\n2047\t0\tUIKitApplication:com.example.App[9791][rb-legacy]\n12\t0\tcom.apple.other\n",
                "",
            )
            .answer(
                format!("xcrun simctl terminate {UDID} com.example.App"),
                true,
                "",
                "",
            )
            .answer(
                format!("xcrun simctl terminate {UDID} com.apple.Preferences"),
                false,
                "",
                "An error…\nfound nothing to terminate\n",
            );
        let mut d = open(fake).await;
        let apps = d.run(Action::ListApps { include_system: false }).await.unwrap();
        assert_eq!(
            apps,
            json!({"apps": [{"bundle_id": "com.example.App", "name": "Example", "version": "2.1", "type": "user"}]})
        );
        let launched = d
            .run(Action::LaunchApp {
                bundle_id: "com.example.App".into(),
                args: vec!["-x".into()],
                env: vec![("A".into(), "1".into())],
                kill_existing: false,
            })
            .await
            .unwrap();
        assert_eq!(launched, json!({"bundle_id": "com.example.App", "pid": 2047}));
        assert!(
            d.runner
                .ran
                .borrow()
                .iter()
                .any(|l| l.ends_with("com.example.App -x [SIMCTL_CHILD_A=1]"))
        );
        let t = d.run(Action::TerminateApp(AppTarget::Pid(2047))).await.unwrap();
        assert_eq!(t, json!({"terminated": true}));
        let t = d.run(Action::TerminateApp(AppTarget::Pid(12))).await.unwrap();
        assert_eq!(t, json!({"terminated": false}), "not an app process: left alone");
        let t = d
            .run(Action::TerminateApp(AppTarget::BundleId(
                "com.apple.Preferences".into(),
            )))
            .await
            .unwrap();
        assert_eq!(t, json!({"terminated": false}));
        let err = d
            .run(Action::TerminateApp(AppTarget::BundleId("com.example.None".into())))
            .await
            .unwrap_err();
        assert_eq!(err.code, Code::AppNotFound);
        let err = d
            .run(Action::LaunchApp {
                bundle_id: "com.example.None".into(),
                args: vec![],
                env: vec![],
                kill_existing: false,
            })
            .await
            .unwrap_err();
        assert_eq!(err.code, Code::AppNotFound);
    }

    #[tokio::test]
    async fn input_and_ax_tree_are_unsupported() {
        let mut d = open(booted()).await;
        let tap = Action::Tap {
            at: crate::geometry::Point { x: 1.0, y: 1.0 },
            count: 1,
        };
        assert_eq!(d.run(tap).await.unwrap_err().code, Code::Unsupported);
        assert_eq!(d.run(Action::AxTree).await.unwrap_err().code, Code::Unsupported);
    }

    #[tokio::test]
    async fn pairs_a_simulator() {
        let dir = std::env::temp_dir().join(format!("persona-iphone-bridge-sim-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("pairing.record");
        let fake = booted();
        assert!(is_simulator(&fake, UDID).await);
        assert!(!is_simulator(&fake, "00000000-0000-0000-0000-000000000000").await);
        let out = pair(&fake, UDID, &path).await.unwrap();
        assert_eq!(out["transport"], "simulator");
        assert_eq!(out["model"], "iPhone18,1");
        assert!(matches!(
            crate::record::Stored::read(&path).unwrap(),
            crate::record::Stored::Simulator(r) if r.udid == UDID
        ));
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn reads_simctl_output() {
        assert_eq!(parse_launch("com.a.b: 12\n", "com.a.b"), Some(12));
        assert_eq!(parse_launch("other: 12\n", "com.a.b"), None);
        assert_eq!(
            app_of_pid("PID\tStatus\tLabel\n5\t0\tUIKitApplication:com.a[1][x]\n", 5).as_deref(),
            Some("com.a")
        );
        let d = parse_devices(&serde_json::from_str(&devices("Booted")).unwrap());
        assert_eq!(d[0].os_version.as_deref(), Some("27.2"));
    }
}
