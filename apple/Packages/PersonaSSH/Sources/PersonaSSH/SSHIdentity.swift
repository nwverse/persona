import CryptoKit
import Foundation
import NIOSSH

/// This device's SSH key (docs/operations/network.md「Connecting to hub through SSH」): one Ed25519 key per device, made by the app. The app
/// keeps the private half in the keychain (this device only); here it is only ever turned into the lines the user
/// pastes on the server.
public struct SSHIdentity: Sendable {
    public let privateKey: Curve25519.Signing.PrivateKey

    public init(privateKey: Curve25519.Signing.PrivateKey) {
        self.privateKey = privateKey
    }

    /// A new key.
    public static func generate() -> SSHIdentity { SSHIdentity(privateKey: .init()) }

    /// The 32-byte seed the keychain keeps.
    public init(rawRepresentation: Data) throws {
        privateKey = try Curve25519.Signing.PrivateKey(rawRepresentation: rawRepresentation)
    }

    public var rawRepresentation: Data { privateKey.rawRepresentation }

    var nioKey: NIOSSHPrivateKey { NIOSSHPrivateKey(ed25519Key: privateKey) }

    /// The public key in the SSH wire format (`string "ssh-ed25519"`, `string key`): what OpenSSH base64-encodes.
    public var publicKeyBlob: Data {
        SSHWire.string(Data("ssh-ed25519".utf8)) + SSHWire.string(privateKey.publicKey.rawRepresentation)
    }

    /// `ssh-ed25519 AAAA… comment`, one line, as `~/.ssh/id_ed25519.pub` has it.
    public func publicKeyLine(comment: String) -> String {
        let c = Self.comment(comment)
        return "ssh-ed25519 " + publicKeyBlob.base64EncodedString() + (c.isEmpty ? "" : " " + c)
    }

    /// The authorized_keys line that lets this key do nothing but forward to the hub: no shell, no agent, no X11, no
    /// pty (`restrict`), only port forwarding (`port-forwarding`), and only to `host:port` (`permitopen`).
    public func authorizedKeysLine(targetHost: String, targetPort: Int, comment: String) -> String {
        "restrict,port-forwarding,permitopen=\"\(Self.hostPort(targetHost, targetPort))\" " + publicKeyLine(comment: comment)
    }

    /// `SHA256:…`, as `ssh-keygen -lf` prints it.
    public var fingerprint: String { SSHFingerprint.of(blob: publicKeyBlob) }

    /// `host:port`, an IPv6 address in brackets (OpenSSH's permitopen syntax).
    static func hostPort(_ host: String, _ port: Int) -> String {
        host.contains(":") ? "[\(host)]:\(port)" : "\(host):\(port)"
    }

    /// The key's comment: one word (no spaces, quotes or line breaks), so the line stays one line and parses the
    /// same everywhere.
    static func comment(_ s: String) -> String {
        let parts = s.components(separatedBy: CharacterSet.whitespacesAndNewlines.union(CharacterSet(charactersIn: "\"'`")))
        return parts.filter { !$0.isEmpty }.joined(separator: "-")
    }
}

/// SSH host key fingerprints, the way OpenSSH shows them.
public enum SSHFingerprint {
    /// `SHA256:` and the unpadded base64 of the SHA-256 of the key's wire format.
    public static func of(blob: Data) -> String {
        let digest = Data(SHA256.hash(data: blob))
        var b64 = digest.base64EncodedString()
        while b64.hasSuffix("=") { b64.removeLast() }
        return "SHA256:" + b64
    }

    /// A key as NIOSSH hands it over: its fingerprint and its algorithm in the short form OpenSSH prints
    /// (`ED25519`, `ECDSA`).
    static func of(_ key: NIOSSHPublicKey) -> (fingerprint: String, algorithm: String) {
        let line = String(openSSHPublicKey: key)
        let parts = line.split(separator: " ")
        guard parts.count >= 2, let blob = Data(base64Encoded: String(parts[1])) else { return ("", "") }
        let algorithm: String = switch parts[0] {
        case "ssh-ed25519": "ED25519"
        case let p where p.hasPrefix("ecdsa-sha2-"): "ECDSA"
        default: String(parts[0])
        }
        return (of(blob: blob), algorithm)
    }
}

enum SSHWire {
    /// An SSH `string`: its length as a big-endian uint32, then the bytes.
    static func string(_ data: Data) -> Data {
        var n = UInt32(data.count).bigEndian
        return Data(bytes: &n, count: 4) + data
    }
}
