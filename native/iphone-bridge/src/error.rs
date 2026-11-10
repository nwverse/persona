//! Error codes of the wire contract, and how the library's errors map to them.

use idevice::IdeviceError;
use idevice::remote_pairing::errors::RemotePairingError;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Code {
    BadRequest,
    PairingMissing,
    PairingRejected,
    Unreachable,
    WrongDevice,
    TunnelFailed,
    DeveloperModeOff,
    DdiNotMounted,
    ServiceUnavailable,
    Locked,
    AppNotFound,
    Unsupported,
    Disconnected,
    Timeout,
    TrustPending,
    NotFound,
    DdiMissing,
    DdiUnsupportedDevice,
    TssFailed,
    MountFailed,
    Internal,
}

impl Code {
    pub fn as_str(self) -> &'static str {
        match self {
            Code::BadRequest => "bad_request",
            Code::PairingMissing => "pairing_missing",
            Code::PairingRejected => "pairing_rejected",
            Code::Unreachable => "unreachable",
            Code::WrongDevice => "wrong_device",
            Code::TunnelFailed => "tunnel_failed",
            Code::DeveloperModeOff => "developer_mode_off",
            Code::DdiNotMounted => "ddi_not_mounted",
            Code::ServiceUnavailable => "service_unavailable",
            Code::Locked => "locked",
            Code::AppNotFound => "app_not_found",
            Code::Unsupported => "unsupported",
            Code::Disconnected => "disconnected",
            Code::Timeout => "timeout",
            Code::TrustPending => "trust_pending",
            Code::NotFound => "not_found",
            Code::DdiMissing => "ddi_missing",
            Code::DdiUnsupportedDevice => "ddi_unsupported_device",
            Code::TssFailed => "tss_failed",
            Code::MountFailed => "mount_failed",
            Code::Internal => "internal",
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct BridgeError {
    pub code: Code,
    pub message: String,
}

impl BridgeError {
    pub fn new(code: Code, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }

    pub fn bad_request(message: impl Into<String>) -> Self {
        Self::new(Code::BadRequest, message)
    }

    pub fn internal(message: impl Into<String>) -> Self {
        Self::new(Code::Internal, message)
    }

    /// Whether the link to the phone is gone: `serve` stops after answering.
    pub fn link_lost(&self) -> bool {
        self.code == Code::Disconnected
    }
}

impl std::fmt::Display for BridgeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code.as_str(), self.message)
    }
}

/// When an error happened, which decides what a transport failure means.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Phase {
    /// Verifying the pairing record over the RemotePairing connection.
    Pairing,
    /// Opening the encrypted tunnel or the service directory.
    Tunnel,
    /// Running an operation over an established tunnel.
    Operation,
}

/// Maps a library error to the contract's code. `what` names the step, for the message.
pub fn from_idevice(err: IdeviceError, phase: Phase, what: &str) -> BridgeError {
    let message = format!("{what}: {err}");
    let code = match (&err, phase) {
        (IdeviceError::RemotePairing(e), _) => match e {
            RemotePairingError::PairingRejected(_)
            | RemotePairingError::PairVerifyFailed
            | RemotePairingError::SrpAuthFailed => Code::PairingRejected,
            _ => Code::TunnelFailed,
        },
        (IdeviceError::InvalidHostID, _) => Code::PairingRejected,
        (IdeviceError::DeveloperModeNotEnabled, _) => Code::DeveloperModeOff,
        (IdeviceError::DeviceLocked | IdeviceError::PasswordProtected, _) => Code::Locked,
        (IdeviceError::PairingDialogResponsePending, _) => Code::TrustPending,
        (IdeviceError::UserDeniedPairing, _) => Code::PairingRejected,
        (IdeviceError::Timeout, _) => Code::Timeout,
        (IdeviceError::ServiceNotFound, _) => Code::ServiceUnavailable,
        (IdeviceError::DeviceNotFound, _) => Code::NotFound,
        (IdeviceError::Socket(_), Phase::Operation) => Code::Disconnected,
        (IdeviceError::Socket(_), Phase::Pairing) => Code::Unreachable,
        (_, Phase::Pairing) => Code::PairingRejected,
        (_, Phase::Tunnel) => Code::TunnelFailed,
        _ => Code::Internal,
    };
    BridgeError::new(code, message)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn io() -> IdeviceError {
        IdeviceError::Socket(std::io::Error::new(std::io::ErrorKind::BrokenPipe, "gone"))
    }

    #[test]
    fn transport_failures_depend_on_the_phase() {
        assert_eq!(from_idevice(io(), Phase::Operation, "tap").code, Code::Disconnected);
        assert_eq!(from_idevice(io(), Phase::Pairing, "verify").code, Code::Unreachable);
        assert_eq!(from_idevice(io(), Phase::Tunnel, "tunnel").code, Code::TunnelFailed);
        assert!(from_idevice(io(), Phase::Operation, "tap").link_lost());
    }

    #[test]
    fn device_answers_keep_their_meaning() {
        let e = from_idevice(IdeviceError::DeveloperModeNotEnabled, Phase::Operation, "hid");
        assert_eq!(e.code, Code::DeveloperModeOff);
        assert!(e.message.starts_with("hid: "));
        let e = from_idevice(
            IdeviceError::RemotePairing(RemotePairingError::PairVerifyFailed),
            Phase::Pairing,
            "verify",
        );
        assert_eq!(e.code, Code::PairingRejected);
        assert_eq!(
            from_idevice(IdeviceError::ServiceNotFound, Phase::Operation, "x").code,
            Code::ServiceUnavailable
        );
        assert_eq!(
            from_idevice(IdeviceError::UnexpectedResponse("?".into()), Phase::Operation, "x").code,
            Code::Internal
        );
    }

    #[test]
    fn codes_are_snake_case() {
        assert_eq!(Code::DdiNotMounted.as_str(), "ddi_not_mounted");
        assert_eq!(Code::AppNotFound.as_str(), "app_not_found");
    }
}
