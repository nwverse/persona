//! `mount_ddi`: mount the personalized developer disk image the phone's developer services (screenshots,
//! input, apps) need. The image comes from Xcode's copy on this computer; personalizing it for one phone takes
//! a ticket the phone may already hold, else one from Apple's signing server (TSS). Nothing is downloaded and
//! nothing about the phone is written anywhere.
//!
//! The steps are written against `Mounter`, so they can be tested without a phone; `device.rs` implements it
//! over the phone's image mounter service. Not verified on a real device.

use std::ffi::OsString;
use std::path::{Component, Path, PathBuf};

use serde_json::{Value, json};
use sha2::{Digest, Sha384};

use crate::error::{BridgeError, Code};

/// Where Xcode keeps the personalized developer disk image.
pub const DEFAULT_DIR: &str = "/Library/Developer/DeveloperDiskImages/iOS_DDI/Restore";
pub const DIR_ENV: &str = "PERSONA_IPHONE_DDI_DIR";
/// What the phone calls the personalized image.
pub const IMAGE_TYPE: &str = "Personalized";

/// The directory to use: the request's, else `$PERSONA_IPHONE_DDI_DIR`, else Xcode's.
pub fn resolve_dir(arg: Option<&str>, env: Option<OsString>) -> PathBuf {
    match (arg, env) {
        (Some(a), _) => PathBuf::from(a),
        (None, Some(e)) if !e.is_empty() => PathBuf::from(e),
        _ => PathBuf::from(DEFAULT_DIR),
    }
}

fn missing(dir: &Path, what: impl std::fmt::Display) -> BridgeError {
    BridgeError::new(
        Code::DdiMissing,
        format!(
            "{what} in {}: this needs Xcode's developer disk image, or pass ddi_dir / set {DIR_ENV} to a folder \
             holding BuildManifest.plist and the images",
            dir.display()
        ),
    )
}

/// The build manifest, as read and as bytes (the signing request needs the dictionary).
pub fn read_manifest(dir: &Path) -> Result<plist::Dictionary, BridgeError> {
    let path = dir.join("BuildManifest.plist");
    let bytes = std::fs::read(&path).map_err(|e| missing(dir, format!("cannot read BuildManifest.plist ({e})")))?;
    let value: plist::Value =
        plist::from_bytes(&bytes).map_err(|_| missing(dir, "BuildManifest.plist is not a property list"))?;
    value
        .into_dictionary()
        .ok_or_else(|| missing(dir, "BuildManifest.plist is not a dictionary"))
}

/// "0x0C" or 12.
fn number(v: Option<&plist::Value>) -> Option<u64> {
    match v? {
        plist::Value::String(s) => {
            let s = s.trim();
            match s.strip_prefix("0x").or_else(|| s.strip_prefix("0X")) {
                Some(hex) => u64::from_str_radix(hex, 16).ok(),
                None => s.parse().ok(),
            }
        }
        plist::Value::Integer(i) => i.as_unsigned(),
        _ => None,
    }
}

/// The image and trust cache (paths relative to the directory) of the build identity for this phone.
pub fn pick(manifest: &plist::Dictionary, board: u64, chip: u64) -> Result<(String, String), BridgeError> {
    let identities = manifest
        .get("BuildIdentities")
        .and_then(plist::Value::as_array)
        .map(Vec::as_slice)
        .unwrap_or_default();
    for id in identities.iter().filter_map(plist::Value::as_dictionary) {
        if number(id.get("ApBoardID")) != Some(board) || number(id.get("ApChipID")) != Some(chip) {
            continue;
        }
        let path = |key: &str| {
            id.get("Manifest")
                .and_then(plist::Value::as_dictionary)
                .and_then(|m| m.get(key))
                .and_then(plist::Value::as_dictionary)
                .and_then(|e| e.get("Info"))
                .and_then(plist::Value::as_dictionary)
                .and_then(|i| i.get("Path"))
                .and_then(plist::Value::as_string)
                .map(str::to_owned)
        };
        if let (Some(image), Some(trust)) = (path("PersonalizedDMG"), path("LoadableTrustCache")) {
            return Ok((image, trust));
        }
    }
    Err(BridgeError::new(
        Code::DdiUnsupportedDevice,
        format!(
            "the developer disk image has no build for this phone (board {board:#x}, chip {chip:#x}); a newer Xcode may have one"
        ),
    ))
}

/// Reads a file the manifest names, which must stay inside the directory.
fn read_inside(dir: &Path, rel: &str) -> Result<Vec<u8>, BridgeError> {
    let p = Path::new(rel);
    if rel.is_empty() || p.components().any(|c| !matches!(c, Component::Normal(_))) {
        return Err(missing(dir, format!("BuildManifest.plist names an odd path {rel:?}")));
    }
    std::fs::read(dir.join(p)).map_err(|e| missing(dir, format!("cannot read {rel} ({e})")))
}

/// The phone side of mounting.
#[allow(async_fn_in_trait)]
pub trait Mounter {
    /// Whether a personalized image is mounted now.
    async fn mounted(&mut self) -> Result<bool, BridgeError>;
    async fn developer_mode(&mut self) -> Result<Option<bool>, BridgeError>;
    /// The phone's board and chip IDs.
    async fn identifiers(&mut self) -> Result<(u64, u64), BridgeError>;
    /// A personalization ticket the phone already holds for the image with this SHA-384, if any.
    async fn device_ticket(&mut self, image_hash: Vec<u8>) -> Result<Option<Vec<u8>>, BridgeError>;
    /// A ticket from Apple's signing server.
    async fn signed_ticket(&mut self, manifest: &plist::Dictionary) -> Result<Vec<u8>, BridgeError>;
    async fn upload_and_mount(&mut self, image: Vec<u8>, ticket: Vec<u8>, trust_cache: Vec<u8>)
    -> Result<(), BridgeError>;
}

pub async fn mount<M: Mounter>(m: &mut M, dir: &Path, force: bool) -> Result<Value, BridgeError> {
    if !force && m.mounted().await? {
        return Ok(json!({"mounted": true, "already": true}));
    }
    if m.developer_mode().await? == Some(false) {
        return Err(BridgeError::new(
            Code::DeveloperModeOff,
            "Developer Mode is off on the phone: Settings › Privacy & Security › Developer Mode",
        ));
    }
    let manifest = read_manifest(dir)?;
    let (board, chip) = m.identifiers().await?;
    let (image_name, trust_name) = pick(&manifest, board, chip)?;
    let image = read_inside(dir, &image_name)?;
    let trust_cache = read_inside(dir, &trust_name)?;
    let hash = Sha384::digest(&image).to_vec();
    let (ticket, source) = match m.device_ticket(hash).await? {
        Some(t) => (t, "phone"),
        None => (m.signed_ticket(&manifest).await?, "apple"),
    };
    m.upload_and_mount(image, ticket, trust_cache).await?;
    if !m.mounted().await? {
        return Err(BridgeError::new(
            Code::MountFailed,
            "the phone accepted the image but does not list it as mounted",
        ));
    }
    Ok(json!({"mounted": true, "already": false, "image": image_name, "ticket": source}))
}

#[cfg(test)]
mod tests {
    use super::*;

    const BOARD: u64 = 0x0C;
    const CHIP: u64 = 0x8101;

    fn identity(board: &str, chip: &str, image: &str) -> plist::Value {
        let entry = |p: &str| {
            let mut info = plist::Dictionary::new();
            info.insert("Path".into(), p.into());
            let mut e = plist::Dictionary::new();
            e.insert("Info".into(), plist::Value::Dictionary(info));
            plist::Value::Dictionary(e)
        };
        let mut m = plist::Dictionary::new();
        m.insert("PersonalizedDMG".into(), entry(image));
        m.insert("LoadableTrustCache".into(), entry(&format!("Firmware/{image}.trustcache")));
        let mut id = plist::Dictionary::new();
        id.insert("ApBoardID".into(), board.into());
        id.insert("ApChipID".into(), chip.into());
        id.insert("Manifest".into(), plist::Value::Dictionary(m));
        plist::Value::Dictionary(id)
    }

    fn manifest(ids: Vec<plist::Value>) -> plist::Dictionary {
        let mut d = plist::Dictionary::new();
        d.insert("BuildIdentities".into(), plist::Value::Array(ids));
        d
    }

    /// A made-up DDI folder: two images for two chip families.
    fn ddi_dir() -> PathBuf {
        let dir = std::env::temp_dir().join(format!("persona-ddi-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(dir.join("Firmware")).unwrap();
        let m = manifest(vec![identity("0x0C", "0x8010", "a.dmg"), identity("0x0C", "0x8101", "b.dmg")]);
        plist::Value::Dictionary(m)
            .to_file_xml(dir.join("BuildManifest.plist"))
            .unwrap();
        for f in ["a.dmg", "b.dmg"] {
            std::fs::write(dir.join(f), format!("image {f}")).unwrap();
            std::fs::write(dir.join("Firmware").join(format!("{f}.trustcache")), format!("trust {f}")).unwrap();
        }
        dir
    }

    #[derive(Default)]
    struct Fake {
        mounted: bool,
        developer_mode: Option<bool>,
        ids: Option<(u64, u64)>,
        phone_ticket: Option<Vec<u8>>,
        signing: Option<BridgeError>,
        refuse_mount: Option<BridgeError>,
        /// Mounting succeeds but the phone still lists nothing.
        silent: bool,
        log: Vec<String>,
    }

    impl Mounter for Fake {
        async fn mounted(&mut self) -> Result<bool, BridgeError> {
            self.log.push("lookup".into());
            Ok(self.mounted)
        }
        async fn developer_mode(&mut self) -> Result<Option<bool>, BridgeError> {
            Ok(self.developer_mode)
        }
        async fn identifiers(&mut self) -> Result<(u64, u64), BridgeError> {
            self.ids
                .ok_or_else(|| BridgeError::internal("the phone did not say its board and chip"))
        }
        async fn device_ticket(&mut self, image_hash: Vec<u8>) -> Result<Option<Vec<u8>>, BridgeError> {
            assert_eq!(image_hash.len(), 48, "SHA-384");
            self.log.push("phone ticket".into());
            Ok(self.phone_ticket.clone())
        }
        async fn signed_ticket(&mut self, manifest: &plist::Dictionary) -> Result<Vec<u8>, BridgeError> {
            assert!(manifest.contains_key("BuildIdentities"));
            self.log.push("apple ticket".into());
            match self.signing.clone() {
                Some(e) => Err(e),
                None => Ok(b"ticket".to_vec()),
            }
        }
        async fn upload_and_mount(
            &mut self,
            image: Vec<u8>,
            ticket: Vec<u8>,
            trust_cache: Vec<u8>,
        ) -> Result<(), BridgeError> {
            self.log.push(format!(
                "mount {} {} {}",
                String::from_utf8_lossy(&image),
                String::from_utf8_lossy(&ticket),
                String::from_utf8_lossy(&trust_cache)
            ));
            if let Some(e) = self.refuse_mount.clone() {
                return Err(e);
            }
            self.mounted = !self.silent;
            Ok(())
        }
    }

    fn phone() -> Fake {
        Fake {
            developer_mode: Some(true),
            ids: Some((BOARD, CHIP)),
            ..Fake::default()
        }
    }

    #[test]
    fn finds_the_directory() {
        assert_eq!(resolve_dir(Some("/a"), Some("/b".into())), PathBuf::from("/a"));
        assert_eq!(resolve_dir(None, Some("/b".into())), PathBuf::from("/b"));
        assert_eq!(resolve_dir(None, Some("".into())), PathBuf::from(DEFAULT_DIR));
        assert_eq!(resolve_dir(None, None), PathBuf::from(DEFAULT_DIR));
    }

    #[test]
    fn picks_the_build_for_this_phone() {
        let m = manifest(vec![identity("0x0C", "0x8010", "a.dmg"), identity("0x0C", "0x8101", "b.dmg")]);
        assert_eq!(
            pick(&m, 0x0C, 0x8101).unwrap(),
            ("b.dmg".to_owned(), "Firmware/b.dmg.trustcache".to_owned())
        );
        assert_eq!(pick(&m, 0x0E, 0x8101).unwrap_err().code, Code::DdiUnsupportedDevice);
        assert_eq!(pick(&plist::Dictionary::new(), 1, 1).unwrap_err().code, Code::DdiUnsupportedDevice);
        assert_eq!(number(Some(&plist::Value::String("12".into()))), Some(12));
        assert_eq!(number(Some(&plist::Value::String("0xzz".into()))), None);
    }

    #[test]
    fn stays_inside_the_directory() {
        let dir = ddi_dir();
        assert!(read_inside(&dir, "a.dmg").is_ok());
        for odd in ["../a.dmg", "/etc/hosts", "", "Firmware/../../x"] {
            assert_eq!(read_inside(&dir, odd).unwrap_err().code, Code::DdiMissing, "{odd}");
        }
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn mounts_with_the_phones_own_ticket_first() {
        let dir = ddi_dir();
        let mut p = phone();
        p.phone_ticket = Some(b"kept".to_vec());
        let r = mount(&mut p, &dir, false).await.unwrap();
        assert_eq!(r, json!({"mounted": true, "already": false, "image": "b.dmg", "ticket": "phone"}));
        assert_eq!(
            p.log,
            ["lookup", "phone ticket", "mount image b.dmg kept trust b.dmg", "lookup"]
        );
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn asks_apple_when_the_phone_has_no_ticket() {
        let dir = ddi_dir();
        let mut p = phone();
        let r = mount(&mut p, &dir, false).await.unwrap();
        assert_eq!(r["ticket"], "apple");
        assert!(p.log.contains(&"mount image b.dmg ticket trust b.dmg".to_owned()));

        let mut p = phone();
        p.signing = Some(BridgeError::new(Code::TssFailed, "no answer"));
        assert_eq!(mount(&mut p, &dir, false).await.unwrap_err().code, Code::TssFailed);
        assert!(!p.log.iter().any(|l| l.starts_with("mount")), "nothing uploaded without a ticket");
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn is_idempotent_unless_forced() {
        let dir = ddi_dir();
        let mut p = phone();
        p.mounted = true;
        assert_eq!(
            mount(&mut p, &dir, false).await.unwrap(),
            json!({"mounted": true, "already": true})
        );
        assert_eq!(p.log, ["lookup"]);
        let r = mount(&mut p, &dir, true).await.unwrap();
        assert_eq!(r["already"], false);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn says_what_is_missing() {
        let mut p = phone();
        let err = mount(&mut p, Path::new("/nonexistent/persona-ddi"), false).await.unwrap_err();
        assert_eq!(err.code, Code::DdiMissing);
        assert!(err.message.contains(DIR_ENV));

        let dir = ddi_dir();
        let mut p = phone();
        p.developer_mode = Some(false);
        assert_eq!(mount(&mut p, &dir, false).await.unwrap_err().code, Code::DeveloperModeOff);

        let mut p = phone();
        p.ids = Some((0x0E, 0x9999));
        assert_eq!(mount(&mut p, &dir, false).await.unwrap_err().code, Code::DdiUnsupportedDevice);

        let mut p = phone();
        p.ids = None;
        assert_eq!(mount(&mut p, &dir, false).await.unwrap_err().code, Code::Internal);

        let mut p = phone();
        p.refuse_mount = Some(BridgeError::new(Code::MountFailed, "refused"));
        assert_eq!(mount(&mut p, &dir, false).await.unwrap_err().code, Code::MountFailed);

        let mut p = phone();
        p.silent = true;
        assert_eq!(mount(&mut p, &dir, false).await.unwrap_err().code, Code::MountFailed);

        std::fs::remove_file(dir.join("b.dmg")).unwrap();
        let mut p = phone();
        assert_eq!(mount(&mut p, &dir, false).await.unwrap_err().code, Code::DdiMissing);
        std::fs::remove_dir_all(dir).unwrap();
    }
}
