import SwiftUI

/// Settings and the confirmation both speak for the hub that owns the queue.
private func countLabel(_ link: HubLink?) -> String? {
    guard let link, let source = link.phoneWriteSource, let count = link.phoneWrites.count(for: source) else { return nil }
    return String(localized: "\(count) phone writes awaiting confirmation on this hub")
}

extension View {
    func followingPhoneWrites(_ link: HubLink?) -> some View {
        let source = link?.phoneWriteSource
        return tabActiveTask(id: source) {
            guard let link, let source, link.phoneWriteSource == source else { return }
            await link.phoneWrites.follow(source, using: link.api)
        }
    }
}

struct PhoneWriteQueueNotice: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if let text = countLabel(model.hub) {
            Section {
                NoticeBanner(text: text, symbol: Theme.Icon.time, lineLimit: nil)
                    .foregroundStyle(Color.primary, Color.primary)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(text)
                    .accessibilityIdentifier("phone-write-queue-count")
                    .bareListRow()
            }
        }
    }
}

struct PhoneWriteConfirmationAlert: ViewModifier {
    var model: AppModel

    func body(content: Content) -> some View {
        let current = model.pendingConfirmation
        #if os(iOS)
        let link = current?.hub.flatMap { source in model.links.first { ObjectIdentifier($0) == source } }
        #else
        let link: HubLink? = nil
        #endif
        let label = countLabel(link)
        content
            #if os(iOS)
            .background {
                if let current {
                    LivePhoneWriteAlertMessage(title: String(localized: "Claude Wants to Act on Your iPhone"),
                                              summary: current.summary, message: message(current, label))
                        .id(current.id)
                        .allowsHitTesting(false)
                        .accessibilityHidden(true)
                        .followingPhoneWrites(link)
                }
            }
            #endif
            .alert("Claude Wants to Act on Your iPhone", isPresented: Binding(
                get: { model.pendingConfirmation != nil },
                // A dismissal belongs to the displayed call, even if the next one is already waiting (#4456).
                set: { [shown = current?.id] in if !$0, let shown { model.phoneConfirmations.answer(shown, false) } }
            ), presenting: current) { confirmation in
                Button("Allow [Notification Actions ce7ef2]") { model.phoneConfirmations.answer(confirmation.id, true) }
                Button("Deny", role: .cancel) { model.phoneConfirmations.answer(confirmation.id, false) }
            } message: { confirmation in
                Text(message(confirmation, label))
            }
    }

    private func message(_ confirmation: PhoneConfirmation, _ label: String?) -> String {
        [confirmation.summary, label].compactMap { $0 }.joined(separator: "\n\n")
    }
}

#if os(iOS)
import UIKit

/// SwiftUI captures an alert's message when it first appears. An authoritative count may arrive later.
private struct LivePhoneWriteAlertMessage: UIViewRepresentable {
    let title: String
    let summary: String
    let message: String

    final class Anchor: UIView {
        var latest: String?
        var update: Task<Void, Never>?

        /// A full-screen Settings cover can leave the presenting root out of the window during its dismissal.
        var sceneForAlert: UIWindowScene? {
            if let scene = window?.windowScene { return scene }
            let active = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
                .filter { $0.activationState == .foregroundActive }
            return active.count == 1 ? active.first : nil
        }
    }

    func makeUIView(context: Context) -> Anchor { Anchor() }

    func updateUIView(_ view: Anchor, context: Context) {
        guard view.latest != message else { return }
        view.latest = message
        view.update?.cancel()
        view.update = Task { @MainActor [weak view] in
            // A fast Hub can answer before SwiftUI has presented its alert over the previous sheet.
            for delay in [0, 50, 100, 200, 400, 800] {
                if delay > 0 { try? await Task.sleep(for: .milliseconds(delay)) }
                guard !Task.isCancelled, let view, view.latest == message else { return }
                for window in view.sceneForAlert?.windows ?? [] {
                    var top = window.rootViewController
                    while let presented = top?.presentedViewController { top = presented }
                    guard let alert = top as? UIAlertController, !alert.isBeingDismissed, alert.title == title,
                          let previous = alert.message else { continue }
                    if previous == message { return }
                    guard previous == summary || previous.hasPrefix(summary + "\n\n") else { continue }
                    alert.message = message
                    return
                }
            }
        }
    }

    static func dismantleUIView(_ view: Anchor, coordinator: Void) { view.update?.cancel() }
}
#endif
