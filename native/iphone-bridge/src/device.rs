//! The phone over RemotePairing: verify the pairing record, open the encrypted tunnel with a userspace TCP
//! stack (no interface, route or DNS change on this computer), read the service directory (RSD), then run
//! operations on the developer services it lists.
//!
//! Not verified on a real device. Everything here follows the library's own descriptions of the services.

use std::net::{IpAddr, SocketAddr};
use std::path::Path;
use std::time::{Duration, Instant};

use idevice::core_device::{
    AppServiceClient, ButtonState, CallInfoBlob, DisplayServiceClient, ImageFormat as DeviceImageFormat,
    IndigoHidClient, ScreenCaptureServiceClient, TOUCHSCREEN_STATE_CONTACT, TOUCHSCREEN_STATE_RELEASE,
    UniversalHidServiceClient, build_screen_audio_offer, build_screen_video_offer, build_start_audio_parameters,
    build_start_video_parameters,
};
use idevice::lockdown::LockdownClient;
use idevice::remote_pairing::{RemotePairingClient, RpPairingSocket, connect_tls_psk_tunnel_native};
use idevice::rsd::RsdHandshake;
use idevice::springboardservices::{InterfaceOrientation, SpringBoardServicesClient};
use idevice::tcp::adapter::Adapter;
use idevice::tcp::handle::AdapterHandle;
use idevice::{IdeviceError, ReadWrite, RsdService};
use serde_json::{Map, Value, json};
use tokio::net::TcpStream;
use tokio::task::JoinHandle;

use crate::error::{BridgeError, Code, Phase, from_idevice};
use crate::geometry::{self, Orientation, Point, Screen};
use crate::keys::{LEFT_SHIFT, Stroke};
use crate::ops::{Action, AppTarget, ImageFormat, KeyInput, ShotOptions, SwipeSpec};
use crate::record::Record;
use crate::run::Phone;
use crate::shot;

/// How long the input gate stays open after the last input, so a run of taps doesn't reopen it each time.
const GATE_IDLE: Duration = Duration::from_secs(10);
/// After the gate opens, the phone needs a moment before input counts as real (the library's description).
const GATE_SETTLE: Duration = Duration::from_millis(300);
/// How long a finger rests for a tap, and between the taps of a double tap.
const TAP_HOLD: Duration = Duration::from_millis(50);
const TAP_GAP: Duration = Duration::from_millis(80);
const KEY_GAP: Duration = Duration::from_millis(15);
/// What this computer says it is when opening the display stream. Values the phone accepts from a Mac.
const CLIENT_SUPPORTED_FEATURES: u64 = 140;
const SIGKILL: u32 = 9;
const DEVELOPER_MODE: (&str, Option<&str>) = ("DeveloperModeStatus", Some("com.apple.security.mac.amfi"));
/// Stopping the display stream when idle must not hang `serve` on a link that has gone quiet.
const STOP_TIMEOUT: Duration = Duration::from_secs(3);

/// Where to reach the phone.
pub struct Target {
    pub udid: String,
    pub host: String,
    pub port: u16,
}

pub struct Device {
    udid: String,
    adapter: AdapterHandle,
    rsd: RsdHandshake,
    /// The RemotePairing control connection: kept for as long as the tunnel it opened.
    _control: RemotePairingClient<RpPairingSocket<TcpStream>>,
    screen: Option<Screen>,
    hid: Option<UniversalHidServiceClient<Box<dyn ReadWrite>>>,
    indigo: Option<IndigoHidClient<Box<dyn ReadWrite>>>,
    gate: Option<Gate>,
    apps: Option<AppServiceClient<Box<dyn ReadWrite>>>,
    springboard: Option<SpringBoardServicesClient>,
}

/// The phone takes synthetic input only while a display stream is running; its video is read and dropped.
struct Gate {
    client: DisplayServiceClient<Box<dyn ReadWrite>>,
    drains: Vec<JoinHandle<()>>,
    last_used: Instant,
}

/// Resolves `host` (an IP literal, an IPv6 literal with `%zone`, or a name) and `port`.
async fn socket_addr(host: &str, port: u16) -> Result<SocketAddr, BridgeError> {
    let unreachable = |why: String| BridgeError::new(Code::Unreachable, format!("{host}: {why}"));
    let bare = host.trim_start_matches('[').trim_end_matches(']');
    if let Some((ip, zone)) = bare.split_once('%') {
        let ip: std::net::Ipv6Addr = ip.parse().map_err(|_| unreachable("not an IPv6 address".into()))?;
        let scope = zone_index(zone).ok_or_else(|| unreachable(format!("unknown interface {zone:?}")))?;
        return Ok(SocketAddr::V6(std::net::SocketAddrV6::new(ip, port, 0, scope)));
    }
    if let Ok(ip) = bare.parse::<IpAddr>() {
        return Ok(SocketAddr::new(ip, port));
    }
    tokio::net::lookup_host((bare, port))
        .await
        .map_err(|e| unreachable(e.to_string()))?
        .next()
        .ok_or_else(|| unreachable("no address".into()))
}

fn zone_index(zone: &str) -> Option<u32> {
    if let Ok(n) = zone.parse() {
        return Some(n);
    }
    #[cfg(unix)]
    {
        let name = std::ffi::CString::new(zone).ok()?;
        // SAFETY: a valid NUL-terminated string; the call only reads it.
        let index = unsafe { libc::if_nametoindex(name.as_ptr()) };
        if index != 0 {
            return Some(index);
        }
    }
    None
}

async fn dial(addr: SocketAddr, what: &str) -> Result<TcpStream, BridgeError> {
    let s = TcpStream::connect(addr)
        .await
        .map_err(|e| BridgeError::new(Code::Unreachable, format!("{what} {addr}: {e}")))?;
    let _ = s.set_nodelay(true);
    Ok(s)
}

impl Device {
    pub async fn connect(target: &Target, mut record: Record) -> Result<Self, BridgeError> {
        let addr = socket_addr(&target.host, target.port).await?;

        // Prove who we are with the saved record. Never fall back to pairing anew: that needs the user.
        let stream = dial(addr, "connect to").await?;
        let mut control = RemotePairingClient::new(RpPairingSocket::new(stream), &record.sending_host);
        control
            .attempt_pair_verify()
            .await
            .map_err(|e| from_idevice(e, Phase::Pairing, "RemotePairing handshake"))?;
        control
            .validate_pairing(&mut record.pairing)
            .await
            .map_err(|e| from_idevice(e, Phase::Pairing, "verify pairing record"))?;

        // The encrypted tunnel, on a port the phone opens for it.
        let port = control
            .create_tcp_listener()
            .await
            .map_err(|e| from_idevice(e, Phase::Tunnel, "open tunnel listener"))?;
        let mut tunnel_addr = addr; // keeps an IPv6 zone
        tunnel_addr.set_port(port);
        let stream = dial(tunnel_addr, "connect tunnel").await?;
        let tunnel = connect_tls_psk_tunnel_native(stream, control.encryption_key())
            .await
            .map_err(|e| from_idevice(e, Phase::Tunnel, "tunnel handshake"))?;
        let info = tunnel.info.clone();
        let parse_ip = |s: &str| {
            s.parse::<IpAddr>()
                .map_err(|_| BridgeError::new(Code::TunnelFailed, format!("tunnel address {s:?}")))
        };
        let mut adapter = Adapter::new(
            Box::new(tunnel.into_inner()),
            parse_ip(&info.client_address)?,
            parse_ip(&info.server_address)?,
        );
        adapter.set_mss(usize::from(info.mtu).saturating_sub(60));
        let mut adapter = adapter.to_async_handle();

        let rsd_stream = adapter
            .connect(info.server_rsd_port)
            .await
            .map_err(|e| from_idevice(e.into(), Phase::Tunnel, "connect service directory"))?;
        let rsd = RsdHandshake::new(rsd_stream)
            .await
            .map_err(|e| from_idevice(e, Phase::Tunnel, "service directory handshake"))?;

        let mut device = Self {
            udid: String::new(),
            adapter,
            rsd,
            _control: control,
            screen: None,
            hid: None,
            indigo: None,
            gate: None,
            apps: None,
            springboard: None,
        };
        let udid = device.udid_reported().await?;
        if !udid.eq_ignore_ascii_case(&target.udid) {
            return Err(BridgeError::new(
                Code::WrongDevice,
                format!("the phone at {} is {udid}, not {}", target.host, target.udid),
            ));
        }
        device.udid = udid;
        Ok(device)
    }

    async fn udid_reported(&mut self) -> Result<String, BridgeError> {
        if let Some(u) = self
            .rsd
            .properties
            .get("UniqueDeviceID")
            .and_then(plist::Value::as_string)
        {
            return Ok(u.to_owned());
        }
        let [v] = self.lockdown_values([("UniqueDeviceID", None)]).await;
        v.as_ref()
            .and_then(plist::Value::as_string)
            .map(str::to_owned)
            .ok_or_else(|| BridgeError::new(Code::TunnelFailed, "the phone did not say which it is"))
    }

    fn has_service<T: RsdService>(&self) -> bool {
        self.rsd.services.contains_key(T::rsd_service_name().as_ref())
    }

    async fn connect_service<T: RsdService>(&mut self, what: &str) -> Result<T, BridgeError> {
        if !self.has_service::<T>() {
            return Err(self.missing_service(what).await);
        }
        T::connect_rsd(&mut self.adapter, &mut self.rsd)
            .await
            .map_err(|e| from_idevice(e, Phase::Operation, what))
    }

    /// Why a developer service is not listed: Developer Mode off, or the developer disk image not mounted.
    async fn missing_service(&mut self, what: &str) -> BridgeError {
        if self.developer_mode().await == Some(false) {
            BridgeError::new(
                Code::DeveloperModeOff,
                format!("{what}: Developer Mode is off on the phone"),
            )
        } else {
            BridgeError::new(
                Code::DdiNotMounted,
                format!("{what}: the phone does not offer it; the developer disk image is probably not mounted"),
            )
        }
    }

    /// Lockdown values `(key, domain)` over one connection, `None` for each that cannot be read.
    async fn lockdown_values<const N: usize>(&mut self, keys: [(&str, Option<&str>); N]) -> [Option<plist::Value>; N] {
        let mut out = [const { None }; N];
        let Ok(mut lockdown) = LockdownClient::connect_rsd(&mut self.adapter, &mut self.rsd).await else {
            return out;
        };
        for (slot, (key, domain)) in out.iter_mut().zip(keys) {
            *slot = lockdown.get_value(Some(key), domain).await.ok();
        }
        out
    }

    async fn developer_mode(&mut self) -> Option<bool> {
        let [v] = self.lockdown_values([DEVELOPER_MODE]).await;
        v.and_then(|v| v.as_boolean())
    }

    /// The interface orientation, portrait when it cannot be read.
    async fn orientation(&mut self) -> Orientation {
        if self.springboard.is_none() {
            self.springboard = SpringBoardServicesClient::connect_rsd(&mut self.adapter, &mut self.rsd)
                .await
                .ok();
        }
        let Some(sb) = self.springboard.as_mut() else {
            return Orientation::Portrait;
        };
        match sb.get_interface_orientation().await {
            Ok(InterfaceOrientation::PortraitUpsideDown) => Orientation::PortraitUpsideDown,
            Ok(InterfaceOrientation::LandscapeRight) => Orientation::LandscapeRight,
            Ok(InterfaceOrientation::LandscapeLeft) => Orientation::LandscapeLeft,
            Ok(_) => Orientation::Portrait,
            Err(_) => {
                self.springboard = None;
                Orientation::Portrait
            }
        }
    }

    /// The panel's size and scale: from the phone's own description, else from a screenshot.
    async fn screen(&mut self) -> Result<Screen, BridgeError> {
        if let Some(s) = self.screen {
            return Ok(s);
        }
        const DOMAIN: Option<&str> = Some("com.apple.mobile.iTunes");
        let num =
            |v: Option<plist::Value>| v.and_then(|v| v.as_real().or_else(|| v.as_unsigned_integer().map(|n| n as f64)));
        let [w, h, scale] = self
            .lockdown_values([
                ("ScreenWidth", DOMAIN),
                ("ScreenHeight", DOMAIN),
                ("ScreenScaleFactor", DOMAIN),
            ])
            .await;
        let screen = match (num(w), num(h), num(scale)) {
            (Some(w), Some(h), Some(scale)) if w > 0.0 && h > 0.0 && scale > 0.0 => Screen {
                pixel_width: w.min(h) as u32,
                pixel_height: w.max(h) as u32,
                scale,
            },
            _ => {
                let png = self.capture(DeviceImageFormat::Png).await?;
                let (w, h) = shot::dimensions(&png)?;
                shot::screen_from_capture(w, h)
            }
        };
        self.screen = Some(screen);
        Ok(screen)
    }

    async fn capture(&mut self, format: DeviceImageFormat) -> Result<Vec<u8>, BridgeError> {
        let mut client: ScreenCaptureServiceClient<Box<dyn ReadWrite>> = self.connect_service("screenshot").await?;
        client
            .take_screenshot(None, format)
            .await
            .map_err(|e| from_idevice(e, Phase::Operation, "screenshot"))
    }

    async fn device_info(&mut self) -> Result<Value, BridgeError> {
        let text = |v: Option<plist::Value>| v.and_then(|v| v.as_string().map(str::to_owned));
        const BATTERY: Option<&str> = Some("com.apple.mobile.battery");
        let [name, product_type, os_version, build, developer_mode, level, charging] = self
            .lockdown_values([
                ("DeviceName", None),
                ("ProductType", None),
                ("ProductVersion", None),
                ("BuildVersion", None),
                DEVELOPER_MODE,
                ("BatteryCurrentCapacity", BATTERY),
                ("BatteryIsCharging", BATTERY),
            ])
            .await;
        let (name, product_type, os_version, build) = (text(name), text(product_type), text(os_version), text(build));
        let developer_mode = developer_mode.and_then(|v| v.as_boolean());
        let level = level.and_then(|v| v.as_unsigned_integer());
        let charging = charging.and_then(|v| v.as_boolean());
        let orientation = self.orientation().await;
        let screen = self.screen().await.ok();
        let frame = screen.map(|s| s.frame(orientation));

        let screenshot = self.has_service::<ScreenCaptureServiceClient<Box<dyn ReadWrite>>>();
        let input = self.has_service::<UniversalHidServiceClient<Box<dyn ReadWrite>>>()
            && self.has_service::<IndigoHidClient<Box<dyn ReadWrite>>>()
            && self.has_service::<DisplayServiceClient<Box<dyn ReadWrite>>>();
        let apps = self.has_service::<AppServiceClient<Box<dyn ReadWrite>>>();
        let missing = if developer_mode == Some(false) {
            Code::DeveloperModeOff
        } else {
            Code::DdiNotMounted
        };
        let mut notes = Map::new();
        for (cap, ok) in [("screenshot", screenshot), ("input", input), ("apps", apps)] {
            if !ok {
                notes.insert(cap.into(), json!(missing.as_str()));
            }
        }
        notes.insert("ax_tree".into(), json!(Code::Unsupported.as_str()));

        Ok(json!({
            "udid": self.udid,
            "name": name,
            "product_type": product_type,
            "os_version": os_version,
            "build": build,
            "point_width": frame.map(|f| f.0),
            "point_height": frame.map(|f| f.1),
            "scale": screen.map(|s| s.scale),
            "orientation": orientation.as_str(),
            "locked": Value::Null,
            "developer_mode": developer_mode,
            // With Developer Mode off nothing is listed, whether the image is mounted or not.
            "ddi_mounted": if developer_mode == Some(false) && !(input || screenshot) {
                None
            } else {
                Some(input || screenshot)
            },
            "battery": level.map(|l| json!({"level": l as f64 / 100.0, "charging": charging})),
            "simulated": false,
            "transport": "network",
            "capabilities": {"screenshot": screenshot, "ax_tree": false, "input": input, "apps": apps},
            "notes": notes,
        }))
    }

    async fn screenshot(&mut self, opts: ShotOptions) -> Result<Value, BridgeError> {
        let orientation = self.orientation().await;
        let screen = self.screen().await?;
        let format = match opts.format {
            ImageFormat::Png => DeviceImageFormat::Png,
            ImageFormat::Jpeg => DeviceImageFormat::Jpeg,
        };
        let raw = self.capture(format).await?;
        let image = shot::prepare(raw, &opts, orientation)?;
        let (pw, ph) = screen.frame(orientation);
        let mut out = json!({
            "mime": image.mime,
            "width": image.width,
            "height": image.height,
            "point_width": pw,
            "point_height": ph,
            "orientation": orientation.as_str(),
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

    /// Opens the input gate if it is not open, and the input services.
    async fn ready_for_input(&mut self) -> Result<(), BridgeError> {
        if self.gate.is_none() {
            let gate = self.open_gate().await?;
            self.gate = Some(gate);
            tokio::time::sleep(GATE_SETTLE).await;
        }
        if self.hid.is_none() {
            self.hid = Some(self.connect_service("touch input").await?);
        }
        if self.indigo.is_none() {
            self.indigo = Some(self.connect_service("key input").await?);
        }
        Ok(())
    }

    async fn open_gate(&mut self) -> Result<Gate, BridgeError> {
        let mut client: DisplayServiceClient<Box<dyn ReadWrite>> = self.connect_service("display stream").await?;
        let fail =
            |what: &str, e: String| BridgeError::new(Code::ServiceUnavailable, format!("display stream: {what}: {e}"));
        let audio = self
            .adapter
            .bind_udp(0)
            .await
            .map_err(|e| fail("bind", e.to_string()))?;
        let video = self
            .adapter
            .bind_udp(0)
            .await
            .map_err(|e| fail("bind", e.to_string()))?;
        let receiver_ip = self.adapter.host_ip().to_string();
        let sender_ip = self.adapter.peer_ip().to_string();
        let call_info = CallInfoBlob {
            call_id: 0,
            client_version: 1,
            device_type: "Mac17,7".into(),
            framework_version: "2205.3.1".into(),
            os_version: "25F71".into(),
            device_name: None,
            audio_device_uid: None,
        };
        let session = uuid::Uuid::new_v4();
        let call_id = || uuid::Uuid::new_v4().to_string().to_uppercase();
        // Audio first: it sets up the screen-sharing session the video stream joins.
        let offer = build_screen_audio_offer(&call_id(), &call_info).map_err(|e| fail("audio offer", e.to_string()))?;
        let params = build_start_audio_parameters(
            &receiver_ip,
            audio.local_port(),
            &sender_ip,
            50000,
            offer,
            CLIENT_SUPPORTED_FEATURES,
            session,
        );
        client
            .start_media_stream(params)
            .await
            .map_err(|e| from_idevice(e, Phase::Operation, "start audio stream"))?;
        let ssrc = uuid::Uuid::new_v4().as_u128() as u32;
        let offer =
            build_screen_video_offer(&call_id(), &call_info, ssrc).map_err(|e| fail("video offer", e.to_string()))?;
        let params = build_start_video_parameters(
            &receiver_ip,
            video.local_port(),
            &sender_ip,
            50001,
            offer,
            CLIENT_SUPPORTED_FEATURES,
            1,
            session,
        );
        if let Err(e) = client.start_media_stream(params).await {
            let _ = client.stop_media_stream().await;
            return Err(from_idevice(e, Phase::Operation, "start video stream"));
        }
        // Read and drop what the phone streams, so nothing piles up in memory.
        let drains = [audio, video]
            .into_iter()
            .map(|socket| tokio::spawn(async move { while socket.recv().await.is_ok() {} }))
            .collect();
        Ok(Gate {
            client,
            drains,
            last_used: Instant::now(),
        })
    }

    /// Stops the display stream; with `link_lost`, only lets go of it, since nothing can reach the phone.
    async fn close_gate(&mut self, link_lost: bool) {
        if let Some(mut gate) = self.gate.take() {
            if !link_lost {
                let _ = tokio::time::timeout(STOP_TIMEOUT, gate.client.stop_media_stream()).await;
            }
            for d in gate.drains {
                d.abort();
            }
        }
    }

    async fn touch(&mut self, x: u16, y: u16, down: bool) -> Result<(), BridgeError> {
        let hid = self.hid.as_mut().expect("ready_for_input connected it");
        let state = if down {
            TOUCHSCREEN_STATE_CONTACT
        } else {
            TOUCHSCREEN_STATE_RELEASE
        };
        hid.send_touchscreen(state, x, y, None)
            .await
            .map_err(|e| from_idevice(e, Phase::Operation, "touch"))
    }

    /// Puts a finger down at the first point, moves it through the rest over `move_ms`, lifts it at the last.
    async fn stroke_path(&mut self, points: &[(u16, u16)], hold: Duration, move_ms: u64) -> Result<(), BridgeError> {
        let (&first, rest) = points.split_first().expect("a path has points");
        self.touch(first.0, first.1, true).await?;
        tokio::time::sleep(hold).await;
        let step = Duration::from_millis(move_ms / rest.len().max(1) as u64);
        for &(x, y) in rest {
            self.touch(x, y, true).await?;
            tokio::time::sleep(step).await;
        }
        let last = points[points.len() - 1];
        self.touch(last.0, last.1, false).await
    }

    async fn gesture(&mut self, action: Action) -> Result<(), BridgeError> {
        let orientation = self.orientation().await;
        let screen = self.screen().await?;
        let map = |p: Point| geometry::to_touch(p, &screen, orientation);
        let to_path = |a: Point, b: Point, ms: u64| -> Result<Vec<(u16, u16)>, BridgeError> {
            let mut pts = vec![map(a)?];
            for p in geometry::path(a, b, ms) {
                pts.push(map(p)?);
            }
            Ok(pts)
        };
        // Every point is checked before the finger goes down, so a bad one never leaves half a gesture.
        enum Plan {
            Taps((u16, u16), u32),
            Press((u16, u16), u64),
            Path(Vec<(u16, u16)>, Duration, u64),
        }
        let plan = match action {
            Action::Tap { at, count } => Plan::Taps(map(at)?, count),
            Action::LongPress { at, duration_ms } => Plan::Press(map(at)?, duration_ms),
            Action::Drag {
                from,
                to,
                duration_ms,
                hold_ms,
            } => Plan::Path(
                to_path(from, to, duration_ms)?,
                Duration::from_millis(hold_ms.max(16)),
                duration_ms,
            ),
            Action::Swipe { spec, duration_ms } => {
                let (a, b) = match spec {
                    SwipeSpec::Ends(a, b) => (a, b),
                    SwipeSpec::Direction { dir, origin, distance } => {
                        geometry::swipe_ends(dir, origin, distance, screen.frame(orientation))
                    }
                };
                Plan::Path(to_path(a, b, duration_ms)?, Duration::from_millis(16), duration_ms)
            }
            _ => unreachable!("only gestures come here"),
        };
        self.ready_for_input().await?;
        match plan {
            Plan::Taps((x, y), count) => {
                for i in 0..count {
                    if i > 0 {
                        tokio::time::sleep(TAP_GAP).await;
                    }
                    self.touch(x, y, true).await?;
                    tokio::time::sleep(TAP_HOLD).await;
                    self.touch(x, y, false).await?;
                }
                Ok(())
            }
            Plan::Press((x, y), ms) => {
                self.touch(x, y, true).await?;
                tokio::time::sleep(Duration::from_millis(ms)).await;
                self.touch(x, y, false).await
            }
            Plan::Path(points, hold, ms) => self.stroke_path(&points, hold, ms).await,
        }
    }

    async fn key(&mut self, usage: u64, down: bool) -> Result<(), BridgeError> {
        let indigo = self.indigo.as_mut().expect("ready_for_input connected it");
        let state = if down { ButtonState::Down } else { ButtonState::Up };
        indigo
            .send_keyboard(usage, state)
            .await
            .map_err(|e| from_idevice(e, Phase::Operation, "key"))
    }

    async fn type_strokes(&mut self, strokes: &[Stroke]) -> Result<(), BridgeError> {
        for s in strokes {
            if s.shift {
                self.key(LEFT_SHIFT, true).await?;
            }
            self.key(s.usage, true).await?;
            self.key(s.usage, false).await?;
            if s.shift {
                self.key(LEFT_SHIFT, false).await?;
            }
            tokio::time::sleep(KEY_GAP).await;
        }
        Ok(())
    }

    async fn keys(&mut self, input: KeyInput) -> Result<(), BridgeError> {
        // Only ASCII can be typed as key presses: refuse anything else before a key goes down.
        let strokes = match &input {
            KeyInput::Text(text) => Some(crate::keys::strokes(text)?),
            KeyInput::Key { .. } => None,
        };
        self.ready_for_input().await?;
        match input {
            KeyInput::Text(_) => self.type_strokes(&strokes.unwrap_or_default()).await,
            KeyInput::Key { usage, repeat, .. } => {
                let one = [Stroke { usage, shift: false }];
                for _ in 0..repeat {
                    self.type_strokes(&one).await?;
                }
                Ok(())
            }
        }
    }

    async fn button(&mut self, button: crate::keys::Button, hold_ms: u64) -> Result<(), BridgeError> {
        self.ready_for_input().await?;
        let indigo = self.indigo.as_mut().expect("ready_for_input connected it");
        let fail = |e| from_idevice(e, Phase::Operation, "button");
        indigo
            .send_button(button.usage_page, button.usage, ButtonState::Down)
            .await
            .map_err(fail)?;
        tokio::time::sleep(Duration::from_millis(hold_ms)).await;
        indigo
            .send_button(button.usage_page, button.usage, ButtonState::Up)
            .await
            .map_err(fail)
    }

    async fn app_service(&mut self) -> Result<&mut AppServiceClient<Box<dyn ReadWrite>>, BridgeError> {
        if self.apps.is_none() {
            self.apps = Some(self.connect_service("app service").await?);
        }
        Ok(self.apps.as_mut().expect("just set"))
    }

    /// Runs an app-service call; drops the cached client on failure, since its stream may be out of step.
    async fn with_apps<T>(
        &mut self,
        what: &str,
        f: impl AsyncFnOnce(&mut AppServiceClient<Box<dyn ReadWrite>>) -> Result<T, IdeviceError>,
    ) -> Result<T, BridgeError> {
        let apps = self.app_service().await?;
        let result = f(apps).await;
        if result.is_err() {
            self.apps = None;
        }
        result.map_err(|e| from_idevice(e, Phase::Operation, what))
    }

    async fn installed(&mut self) -> Result<Vec<idevice::core_device::AppListEntry>, BridgeError> {
        self.with_apps("list apps", async |a| {
            a.list_apps(false, true, false, false, true).await
        })
        .await
    }

    async fn list_apps(&mut self, include_system: bool) -> Result<Value, BridgeError> {
        let apps: Vec<Value> = self
            .installed()
            .await?
            .into_iter()
            .filter(|a| include_system || !a.is_first_party)
            .map(|a| {
                json!({
                    "bundle_id": a.bundle_identifier,
                    "name": a.name,
                    "version": a.version,
                    "type": if a.is_first_party { "system" } else { "user" },
                })
            })
            .collect();
        Ok(json!({ "apps": apps }))
    }

    async fn launch_app(
        &mut self,
        bundle_id: String,
        args: Vec<String>,
        env: Vec<(String, String)>,
        kill_existing: bool,
    ) -> Result<Value, BridgeError> {
        let env: plist::Dictionary = env.into_iter().map(|(k, v)| (k, plist::Value::String(v))).collect();
        let id = bundle_id.clone();
        let launched = self
            .with_apps("launch app", async move |a| {
                let args: Vec<&str> = args.iter().map(String::as_str).collect();
                a.launch_application(id, &args, kill_existing, false, Some(env), None, None)
                    .await
            })
            .await;
        match launched {
            Ok(r) => Ok(json!({"bundle_id": bundle_id, "pid": r.pid})),
            Err(e) => match self.installed().await {
                Ok(apps) if !apps.iter().any(|a| a.bundle_identifier == bundle_id) => Err(BridgeError::new(
                    Code::AppNotFound,
                    format!("no app {bundle_id} on the phone"),
                )),
                _ => Err(e),
            },
        }
    }

    async fn terminate_app(&mut self, target: AppTarget) -> Result<Value, BridgeError> {
        let processes = self
            .with_apps("list processes", async |a| a.list_processes().await)
            .await?;
        let pid = match target {
            AppTarget::Pid(pid) => processes.iter().any(|p| p.pid == pid).then_some(pid),
            AppTarget::BundleId(id) => {
                let apps = self.installed().await?;
                let app = apps
                    .iter()
                    .find(|a| a.bundle_identifier == id)
                    .ok_or_else(|| BridgeError::new(Code::AppNotFound, format!("no app {id} on the phone")))?;
                processes
                    .iter()
                    .find(|p| {
                        p.executable_url
                            .as_ref()
                            .is_some_and(|u| runs_from(&u.relative, &app.path))
                    })
                    .map(|p| p.pid)
            }
        };
        let Some(pid) = pid else {
            return Ok(json!({"terminated": false}));
        };
        self.with_apps("terminate app", async move |a| a.send_signal(pid, SIGKILL).await)
            .await?;
        Ok(json!({"terminated": true}))
    }
}

/// Whether an executable (a `file://` URL, percent-encoded) lies inside an app bundle (a plain path). Either
/// may carry the `/private` prefix of `/var` on iOS.
fn runs_from(executable_url: &str, bundle_path: &str) -> bool {
    fn canonical(path: &str) -> String {
        let path = path.trim_end_matches('/');
        let path = path.strip_prefix("/private").unwrap_or(path);
        format!("{path}/")
    }
    let path = executable_url.strip_prefix("file://").unwrap_or(executable_url);
    canonical(&percent_decode(path)).starts_with(&canonical(bundle_path))
}

fn percent_decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        let hex = |c: u8| (c as char).to_digit(16);
        if b[i] == b'%'
            && i + 2 < b.len()
            && let (Some(h), Some(l)) = (hex(b[i + 1]), hex(b[i + 2]))
        {
            out.push((h * 16 + l) as u8);
            i += 3;
            continue;
        }
        out.push(b[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

impl Phone for Device {
    async fn run(&mut self, action: Action) -> Result<Value, BridgeError> {
        let input = action.is_input();
        let result = match action {
            Action::DeviceInfo => self.device_info().await,
            Action::Screenshot(opts) => self.screenshot(opts).await,
            Action::AxTree { .. } => Err(BridgeError::new(
                Code::Unsupported,
                "the accessibility tree is not available over this connection yet; use screenshot",
            )),
            Action::Keys(k) => self.keys(k).await.map(|()| json!({})),
            Action::Button { button, hold_ms } => self.button(button, hold_ms).await.map(|()| json!({})),
            Action::LaunchApp {
                bundle_id,
                args,
                env,
                kill_existing,
            } => self.launch_app(bundle_id, args, env, kill_existing).await,
            Action::TerminateApp(t) => self.terminate_app(t).await,
            Action::ListApps { include_system } => self.list_apps(include_system).await,
            gesture => self.gesture(gesture).await.map(|()| json!({})),
        };
        if input {
            if let Some(e) = result.as_ref().err().filter(|e| e.code != Code::BadRequest) {
                // The input services may be out of step after a failure: start them afresh next time. A
                // request refused before anything was sent leaves them as they are.
                self.hid = None;
                self.indigo = None;
                self.close_gate(e.link_lost()).await;
            } else if let Some(g) = self.gate.as_mut() {
                g.last_used = Instant::now();
            }
        }
        result
    }

    fn idle_at(&self) -> Option<tokio::time::Instant> {
        self.gate
            .as_ref()
            .map(|g| tokio::time::Instant::from_std(g.last_used + GATE_IDLE))
    }

    async fn idle(&mut self) {
        self.hid = None;
        self.indigo = None;
        self.close_gate(false).await;
    }

    async fn close(&mut self) {
        self.idle().await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn resolves_literals_and_zones() {
        assert_eq!(
            socket_addr("192.0.2.7", 49152).await.unwrap(),
            "192.0.2.7:49152".parse().unwrap()
        );
        assert_eq!(
            socket_addr("[2001:db8::1]", 1).await.unwrap(),
            "[2001:db8::1]:1".parse().unwrap()
        );
        let SocketAddr::V6(v6) = socket_addr("fe80::1%7", 1).await.unwrap() else {
            panic!()
        };
        assert_eq!(v6.scope_id(), 7);
        assert_eq!(
            socket_addr("fe80::1%no-such-interface0", 1).await.unwrap_err().code,
            Code::Unreachable
        );
        assert_eq!(socket_addr("zz::1%1", 1).await.unwrap_err().code, Code::Unreachable);
    }

    #[test]
    fn matches_a_process_to_its_app() {
        let app = "/private/var/containers/Bundle/Application/0000/My App.app";
        assert!(runs_from(
            "file:///var/containers/Bundle/Application/0000/My%20App.app/My%20App",
            app
        ));
        assert!(runs_from(
            "file:///private/var/containers/Bundle/Application/0000/My%20App.app/My%20App",
            "/var/containers/Bundle/Application/0000/My App.app/"
        ));
        assert!(!runs_from(
            "file:///var/containers/Bundle/Application/0000/My%20App.appendix/x",
            app
        ));
        assert_eq!(percent_decode("a%2Fb%zz%4"), "a/b%zz%4");
    }

    fn temp_record() -> (std::path::PathBuf, std::path::PathBuf) {
        let dir = std::env::temp_dir().join(format!("persona-iphone-bridge-device-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("pairing.record");
        Record::generate().write(&path).unwrap();
        (dir, path)
    }

    fn local(port: u16) -> Target {
        Target {
            udid: "U".into(),
            host: "127.0.0.1".into(),
            port,
        }
    }

    async fn connect_err(target: &Target, record: &Path) -> BridgeError {
        let record = match crate::record::Stored::read(record) {
            Ok(crate::record::Stored::Network(r)) => *r,
            Ok(_) => panic!("a network record"),
            Err(e) => return e,
        };
        match Device::connect(target, record).await {
            Ok(_) => panic!("connected to a stand-in"),
            Err(e) => e,
        }
    }

    #[tokio::test]
    async fn nothing_listening_is_unreachable() {
        let (dir, record) = temp_record();
        let port = {
            let l = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
            l.local_addr().unwrap().port()
        };
        assert_eq!(connect_err(&local(port), &record).await.code, Code::Unreachable);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn a_peer_that_is_not_a_phone_fails_cleanly() {
        let (dir, record) = temp_record();
        // One that hangs up at once, and one that answers with garbage.
        for reply in [&b""[..], b"HTTP/1.1 400 Bad Request\r\n\r\n"] {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let port = listener.local_addr().unwrap().port();
            let server = tokio::spawn(async move {
                use tokio::io::AsyncWriteExt as _;
                let (mut s, _) = listener.accept().await.unwrap();
                let _ = s.write_all(reply).await;
            });
            let err = tokio::time::timeout(Duration::from_secs(10), connect_err(&local(port), &record))
                .await
                .expect("gives up instead of hanging");
            assert!(
                matches!(err.code, Code::Unreachable | Code::PairingRejected | Code::TunnelFailed),
                "{err}"
            );
            server.await.unwrap();
        }
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn a_missing_record_fails_before_dialing() {
        let e = connect_err(&local(9), Path::new("/nonexistent/pairing.record")).await;
        assert_eq!(e.code, Code::PairingMissing);
    }
}
