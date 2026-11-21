import Foundation

/// Opt-in lifecycle diagnostics for the native integration tests. Production leaves this unset and writes nothing.
/// No traffic, credentials or keys are recorded.
private let traceLock = NSLock()

func trace(_ message: String) {
    guard let path = ProcessInfo.processInfo.environment["PERSONA_SSH_TRACE"] else { return }
    traceLock.withLock {
        if !FileManager.default.fileExists(atPath: path) { FileManager.default.createFile(atPath: path, contents: nil) }
        guard let handle = FileHandle(forWritingAtPath: path) else { return }
        handle.seekToEndOfFile()
        handle.write(Data((message + "\n").utf8))
        try? handle.close()
    }
}
