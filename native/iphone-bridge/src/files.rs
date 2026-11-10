//! Files only this user can read.

use std::io::Write as _;
use std::path::Path;

use crate::error::BridgeError;

/// Writes `bytes` to `path` readable by this user only, replacing it in one step: a new 0600 file renamed
/// over it, so an existing file's permissions or a symlink at `path` never decide who can read it.
pub fn write_private(path: &Path, bytes: &[u8]) -> Result<(), BridgeError> {
    let fail = |e: std::io::Error| BridgeError::internal(format!("write {}: {e}", path.display()));
    let name = path
        .file_name()
        .ok_or_else(|| BridgeError::bad_request(format!("{} is not a file path", path.display())))?;
    let mut tmp_name = std::ffi::OsString::from(".");
    tmp_name.push(name);
    tmp_name.push(format!(".{}.tmp", uuid::Uuid::new_v4().simple()));
    let tmp = path.with_file_name(tmp_name);
    let mut opts = std::fs::OpenOptions::new();
    opts.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt as _;
        opts.mode(0o600);
    }
    let result = (|| {
        let mut f = opts.open(&tmp)?;
        f.write_all(bytes)?;
        f.sync_all()?;
        std::fs::rename(&tmp, path)
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
    result.map_err(fail)
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt as _;

    #[test]
    fn replaces_whatever_was_there_with_an_owner_only_file() {
        let dir = std::env::temp_dir().join(format!("persona-iphone-bridge-files-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&dir).unwrap();
        let open = dir.join("open.txt");
        std::fs::write(&open, b"old").unwrap();
        std::fs::set_permissions(&open, std::fs::Permissions::from_mode(0o644)).unwrap();
        let path = dir.join("shot.jpg");
        std::os::unix::fs::symlink(&open, &path).unwrap();

        write_private(&path, b"new").unwrap();
        let meta = std::fs::symlink_metadata(&path).unwrap();
        assert!(meta.file_type().is_file(), "the symlink itself was replaced");
        assert_eq!(meta.permissions().mode() & 0o777, 0o600);
        assert_eq!(std::fs::read(&path).unwrap(), b"new");
        assert_eq!(std::fs::read(&open).unwrap(), b"old", "the link's target is untouched");
        assert_eq!(std::fs::read_dir(&dir).unwrap().count(), 2, "no temporary file left");
        std::fs::remove_dir_all(&dir).unwrap();
    }
}
