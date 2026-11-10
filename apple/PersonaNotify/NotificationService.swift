import os
import Foundation
import UserNotifications

/// Opens pushes that came through the relay (docs/operations/push-relay.md): the relay only saw 「有新消息」 and a sealed payload; here
/// the phone decrypts it and shows the real title, text and buttons. Not opened in time, no key yet (the phone has not
/// been unlocked since it started), or a payload that does not open: the fallback stays, never anything in the clear.
final class NotificationService: UNNotificationServiceExtension, @unchecked Sendable {
    private static let log = Logger(subsystem: "persona", category: "notify")
    private let completionLock = NSLock()
    private var contentHandler: ((UNNotificationContent) -> Void)?
    private var fallback: UNNotificationContent?

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        completionLock.withLock {
            self.contentHandler = contentHandler
            fallback = request.content
        }
        // Every group of hubs seals to its own key (docs/protocol/multi-hub.md「Push and notifications」): the one that opens it names the group.
        let (content, outcome) = PushCrypto.service(request.content, keys: PushCrypto.allKeys)
        // Why a push stayed 「有新消息」 is the first question when one does (no content in the log).
        switch outcome {
        case .notSealed: break
        case .noKey: Self.log.error("sealed push: no key (not unlocked since boot, or none made yet)")
        case .unopened: Self.log.error("sealed push: did not open, or too old")
        case .opened: Self.log.notice("sealed push opened")
        }
        // Decryption may return mutable content: copy it into a read-only snapshot first; the async registration shares nothing mutable.
        nonisolated(unsafe) let ready = content.copy() as! UNNotificationContent
        Task { @Sendable [self] in
            await ExtensionNotificationActions.register(content: ready)
            finish(ready)
        }
    }

    override func serviceExtensionTimeWillExpire() {
        if let fallback = completionLock.withLock({ fallback }) { finish(fallback) }
    }

    private func finish(_ content: UNNotificationContent) {
        let handler = completionLock.withLock {
            let handler = contentHandler
            contentHandler = nil
            fallback = nil
            return handler
        }
        handler?(content)
    }
}
