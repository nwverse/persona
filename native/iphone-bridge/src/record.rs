//! The pairing record file: opaque to the node, the bridge's own format.
//!
//! A plist holding the RemotePairing record and the host name it was made under: the phone knows this
//! computer by that name, so every later connection has to send the same one.

use std::hash::{BuildHasher, Hasher};
use std::path::Path;

use idevice::remote_pairing::RpPairingFile;

use crate::error::{BridgeError, Code};

const FORMAT: u64 = 1;
const SIMULATOR: &str = "simulator";

/// What a record file names: a phone reached over the network, or a simulator on this computer (the
/// development and testing backend).
pub enum Stored {
    Network(Box<Record>),
    Simulator(SimRecord),
}

impl Stored {
    pub fn read(path: &Path) -> Result<Self, BridgeError> {
        let bytes = std::fs::read(path).map_err(|e| {
            BridgeError::new(
                Code::PairingMissing,
                format!("read pairing record {}: {e}", path.display()),
            )
        })?;
        Self::from_bytes(&bytes)
    }

    pub fn from_bytes(bytes: &[u8]) -> Result<Self, BridgeError> {
        let value: plist::Value = plist::from_bytes(bytes).map_err(|_| not_a_record("not a plist"))?;
        let d = value.as_dictionary().ok_or_else(|| not_a_record("not a dictionary"))?;
        if d.get("Transport").and_then(plist::Value::as_string) == Some(SIMULATOR) {
            if d.get("Format").and_then(plist::Value::as_unsigned_integer) != Some(FORMAT) {
                return Err(not_a_record("unknown format"));
            }
            let udid = d
                .get("UDID")
                .and_then(plist::Value::as_string)
                .filter(|u| !u.is_empty())
                .ok_or_else(|| not_a_record("no simulator UDID"))?;
            return Ok(Stored::Simulator(SimRecord { udid: udid.to_owned() }));
        }
        Record::from_bytes(bytes).map(|r| Stored::Network(Box::new(r)))
    }
}

/// A simulator "paired" for the development backend: nothing to prove, only which one.
pub struct SimRecord {
    pub udid: String,
}

impl SimRecord {
    pub fn to_bytes(&self) -> Vec<u8> {
        let mut d = plist::Dictionary::new();
        d.insert("Format".into(), plist::Value::Integer(FORMAT.into()));
        d.insert("Transport".into(), plist::Value::String(SIMULATOR.into()));
        d.insert("UDID".into(), plist::Value::String(self.udid.clone()));
        let mut out = Vec::new();
        plist::Value::Dictionary(d)
            .to_writer_xml(&mut out)
            .expect("a dictionary always serializes");
        out
    }

    pub fn write(&self, path: &Path) -> Result<(), BridgeError> {
        crate::files::write_private(path, &self.to_bytes())
    }
}

fn not_a_record(why: &str) -> BridgeError {
    BridgeError::new(Code::PairingMissing, format!("not a pairing record: {why}"))
}

pub struct Record {
    pub sending_host: String,
    pub pairing: RpPairingFile,
}

impl Record {
    /// A new record for pairing: a host name unique to this record, so two computers pairing with the
    /// same phone never replace each other's record on it.
    pub fn generate() -> Self {
        let sending_host = format!("Persona {:08x}", random_u32());
        let pairing = RpPairingFile::generate(&sending_host);
        Self { sending_host, pairing }
    }

    pub fn to_bytes(&self) -> Vec<u8> {
        let mut d = plist::Dictionary::new();
        d.insert("Format".into(), plist::Value::Integer(FORMAT.into()));
        d.insert("SendingHost".into(), plist::Value::String(self.sending_host.clone()));
        d.insert("RemotePairing".into(), plist::Value::Data(self.pairing.to_bytes()));
        let mut out = Vec::new();
        plist::Value::Dictionary(d)
            .to_writer_xml(&mut out)
            .expect("a dictionary always serializes");
        out
    }

    pub fn from_bytes(bytes: &[u8]) -> Result<Self, BridgeError> {
        let bad = not_a_record;
        let value: plist::Value = plist::from_bytes(bytes).map_err(|_| bad("not a plist"))?;
        let d = value.as_dictionary().ok_or_else(|| bad("not a dictionary"))?;
        if d.get("Format").and_then(plist::Value::as_unsigned_integer) != Some(FORMAT) {
            return Err(bad("unknown format"));
        }
        let sending_host = d
            .get("SendingHost")
            .and_then(plist::Value::as_string)
            .ok_or_else(|| bad("no host name"))?
            .to_owned();
        let pairing = d
            .get("RemotePairing")
            .and_then(plist::Value::as_data)
            .ok_or_else(|| bad("no RemotePairing record"))?;
        let pairing = RpPairingFile::from_bytes(pairing).map_err(|_| bad("unreadable RemotePairing record"))?;
        Ok(Self { sending_host, pairing })
    }

    /// Writes the record readable by this user only, replacing `path` in one step.
    pub fn write(&self, path: &Path) -> Result<(), BridgeError> {
        crate::files::write_private(path, &self.to_bytes())
    }
}

/// A random number from the standard library's per-process hash keys: good enough to tell records apart.
fn random_u32() -> u32 {
    let mut h = std::collections::hash_map::RandomState::new().build_hasher();
    h.write_u128(
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or_default(),
    );
    h.write_u32(std::process::id());
    h.finish() as u32
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trips() {
        let r = Record::generate();
        assert!(r.sending_host.starts_with("Persona "));
        let back = Record::from_bytes(&r.to_bytes()).unwrap();
        assert_eq!(back.sending_host, r.sending_host);
        assert_eq!(back.pairing.private_key_bytes(), r.pairing.private_key_bytes());
        assert_eq!(back.pairing.identifier(), r.pairing.identifier());
    }

    #[test]
    fn tells_simulator_records_from_network_ones() {
        let sim = SimRecord { udid: "SIM-1".into() }.to_bytes();
        assert!(matches!(Stored::from_bytes(&sim).unwrap(), Stored::Simulator(r) if r.udid == "SIM-1"));
        let net = Record::generate().to_bytes();
        assert!(matches!(Stored::from_bytes(&net).unwrap(), Stored::Network(_)));
        assert!(
            Record::from_bytes(&sim).is_err(),
            "a simulator record is not a RemotePairing record"
        );
        assert_eq!(Stored::from_bytes(b"x").err().unwrap().code, Code::PairingMissing);
    }

    #[test]
    fn rejects_other_files() {
        assert_eq!(Record::from_bytes(b"hello").err().unwrap().code, Code::PairingMissing);
        let mut d = plist::Dictionary::new();
        d.insert("Format".into(), plist::Value::Integer(9.into()));
        let mut bytes = Vec::new();
        plist::Value::Dictionary(d).to_writer_xml(&mut bytes).unwrap();
        assert!(Record::from_bytes(&bytes).is_err());
        let missing = Stored::read(Path::new("/nonexistent/persona/pairing.record"));
        assert_eq!(missing.err().unwrap().code, Code::PairingMissing);
    }

    #[cfg(unix)]
    #[test]
    fn writes_owner_only() {
        use std::os::unix::fs::PermissionsExt as _;
        let dir = std::env::temp_dir().join(format!("persona-iphone-bridge-test-{:08x}", random_u32()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("pairing.record");
        let r = Record::generate();
        r.write(&path).unwrap();
        r.write(&path).unwrap(); // replaces
        let mode = std::fs::metadata(&path).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o600);
        let Stored::Network(back) = Stored::read(&path).unwrap() else {
            panic!("a network record")
        };
        assert_eq!(back.sending_host, r.sending_host);
        assert_eq!(std::fs::read_dir(&dir).unwrap().count(), 1);
        std::fs::remove_dir_all(&dir).unwrap();
    }
}
