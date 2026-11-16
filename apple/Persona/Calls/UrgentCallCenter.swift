#if os(iOS)
import AVFoundation
import Foundation
import LiveCommunicationKit
import os
import UIKit
import UserNotifications

/// Rings the phone for an urgent call (docs/assistant/urgent-call.md): a VoIP push becomes an incoming call reported to
/// LiveCommunicationKit at once (iOS ends an app that does not report one), with a silent time-sensitive companion
/// notification beside it. On some iPhones sold in mainland China the call shows only as a banner or in the Dynamic
/// Island; the notification stays after the call's banner goes, and tapping it answers. Answering opens the call page
/// (`UrgentCallSession`); declining, a ring nobody answers in 45 seconds, and the call settled elsewhere end it, and
/// the manager hears each.
@MainActor @Observable final class UrgentCallCenter {
    static let shared = UrgentCallCenter()
    private static let log = Logger(subsystem: "persona", category: "urgent-call")

    /// The call the page shows: answered, or opened from its notification.
    private(set) var session: UrgentCallSession?

    /// How long a ring lasts before it counts as missed (the manager rings once more after its retry wait).
    static let ringFor: Duration = .seconds(45)
    /// The companion notification's id for a call.
    nonisolated static func noteID(_ call: String) -> String { "urgent-call-" + call }
    /// The key a companion notification carries, so a tap finds its call.
    nonisolated static let callKey = "urgent_call_id"

    private struct Ringing {
        var ring: UrgentCallRing
        var session: UrgentCallSession
        var answered = false
    }

    @ObservationIgnored private var ringing: [UUID: Ringing] = [:]
    /// LiveCommunicationKit's manager: made once, used only from the main actor (its async calls run on its own queue),
    /// so it is held unchecked rather than sent between regions.
    @ObservationIgnored nonisolated(unsafe) private let manager: ConversationManager
    @ObservationIgnored private let delegate = Delegate()

    private init() {
        let config = ConversationManager.Configuration(ringtoneName: nil, iconTemplateImageData: nil, maximumConversationGroups: 1,
                                                       maximumConversationsPerConversationGroup: 1, includesConversationInRecents: false,
                                                       supportsVideo: false, supportedHandleTypes: [.generic])
        manager = ConversationManager(configuration: config)
        manager.delegate = delegate
    }

    // MARK: Ringing

    /// The name the incoming call shows: "Urgent: <title>" while the lock screen may show details, else only that
    /// it is Persona's urgent call (the notifications' privacy rule).
    nonisolated static func callName(title: String?, details: Bool) -> String {
        guard details, let title, !title.isEmpty else { return String(localized: "Persona Urgent Call") }
        return String(localized: "Urgent: \(title)")
    }

    /// A VoIP push came: report it as a call now, whatever it holds (iOS requires it), then `done`. One that does not
    /// open or is too old is reported and ended at once.
    func incoming(_ payload: [AnyHashable: Any], done: @escaping () -> Void) {
        let uuid = UUID()
        let ring = PushCrypto.notificationInfo(payload).flatMap { UrgentCallRing(info: $0) }
        // "Show Details on Lock Screen" (Settings › Notifications), as the app keeps it for the Live Activity: off (or not
        // read yet), the call and its notification say only that Persona is calling, not what about.
        let details = UserDefaults.standard.bool(forKey: LiveActivityController.detailsKey)
        let name = Self.callName(title: ring?.title, details: details)
        let update = Conversation.Update(localMember: Handle(type: .generic, value: "owner"),
                                         members: [Handle(type: .generic, value: ring?.id ?? uuid.uuidString, displayName: name)])
        prepareAudio()
        if let ring {
            let session = UrgentCallSession(ring: ring) { [weak self] ended in self?.sessionEnded(ended) }
            ringing[uuid] = Ringing(ring: ring, session: session)
            Task { await session.prepare() }
        }
        Task {
            do {
                try await manager.reportNewIncomingConversation(uuid: uuid, update: update)
            } catch {
                // Focus or Do Not Disturb can refuse the call: the companion notification is what the owner sees then.
                Self.log.notice("urgent call: the call was not shown: \(error.localizedDescription, privacy: .public)")
            }
            done()
            guard let ring else {
                end(uuid, .failed)
                return
            }
            await companion(ring, missed: false, details: details)
            try? await Task.sleep(for: Self.ringFor)
            if let r = ringing[uuid], !r.answered { missed(uuid) }
        }
    }

    /// The call session's category, set before the system activates the session for the call.
    private func prepareAudio() {
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .voiceChat, options: PhoneVoiceAudio.sessionOptions)
    }

    /// The owner answered (on the call, or by tapping its notification while it rang).
    fileprivate func answered(_ uuid: UUID) {
        guard var r = ringing[uuid], !r.answered else { return }
        r.answered = true
        ringing[uuid] = r
        session = r.session
        removeCompanion(r.ring.id)
        report(r.ring, .answered)
    }

    /// The call ended on the phone: declined while it rang, or hung up after it was answered.
    fileprivate func endedOnPhone(_ uuid: UUID) {
        guard let r = ringing.removeValue(forKey: uuid) else { return }
        removeCompanion(r.ring.id)
        if r.answered {
            r.session.hangUp()
        } else {
            report(r.ring, .declined)
        }
    }

    /// Nobody answered in time: the call ends, the notification says it was missed, and the manager rings again later
    /// (or pushes the matter).
    private func missed(_ uuid: UUID) {
        guard let r = ringing.removeValue(forKey: uuid) else { return }
        end(uuid, .unanswered)
        report(r.ring, .missed)
        let details = UserDefaults.standard.bool(forKey: LiveActivityController.detailsKey)
        Task { await companion(r.ring, missed: true, details: details) }
    }

    /// The manager says where a call is (`urgent_call`): a phone still ringing for one answered, declined or settled
    /// elsewhere stops.
    func changed(id: String, state: String) {
        guard state != "ringing", let (uuid, r) = ringing.first(where: { $0.value.ring.id == id }), !r.answered else { return }
        ringing[uuid] = nil
        end(uuid, state == "answered" ? .joinedElsewhere : state == "declined" ? .declinedElsewhere : .remoteEnded)
        removeCompanion(id)
    }

    /// The companion notification was tapped: answer the call while it rings, else open its conversation.
    func opened(callID: String, group: String) {
        if let (uuid, r) = ringing.first(where: { $0.value.ring.id == callID }), !r.answered {
            Task {
                do {
                    try await manager.perform([JoinConversationAction(conversationUUID: uuid)])
                } catch {
                    answered(uuid) // no call to join (the system refused it): the page opens all the same
                    audioActivated()
                }
            }
            return
        }
        removeCompanion(callID)
        guard let api = AppModel.shared.api(group) else { return }
        Task {
            guard let info = try? await api.urgentCall(callID), let machine = info.machineID, let conversation = info.conversationID else { return }
            try? await api.reportUrgentCall(callID, .seen)
            AppModel.shared.open(SessionKey(group: group, machineID: machine, sessionID: conversation))
        }
    }

    #if DEBUG
    /// `-showUrgentCallSample YES`: the call page for the mock hub's call `call_mock`, without ringing or audio.
    func showSample() {
        guard let group = AppModel.shared.hub?.group else { return }
        let ring = UrgentCallRing(id: "call_mock", title: "", manager: "", conversationID: "", attempt: 1, group: group)
        let s = UrgentCallSession(ring: ring) { [weak self] _ in self?.session = nil }
        session = s
        Task { await s.sample() }
    }
    #endif

    // MARK: The call's audio

    fileprivate func audioActivated() {
        session?.start()
    }

    fileprivate func audioDeactivated() {}

    /// The page's Hang Up: ends the call with the system, which ends the session.
    func hangUp() {
        guard let session else { return }
        if let (uuid, _) = ringing.first(where: { $0.value.session === session }) {
            Task { try? await manager.perform([EndConversationAction(conversationUUID: uuid)]) }
        } else {
            session.hangUp()
        }
    }

    private func sessionEnded(_ ended: UrgentCallSession) {
        if let (uuid, _) = ringing.first(where: { $0.value.session === ended }) {
            ringing[uuid] = nil
            end(uuid, .remoteEnded)
        }
        if session === ended { session = nil }
    }

    private func end(_ uuid: UUID, _ reason: Conversation.EndedReason) {
        guard let conversation = manager.conversations.first(where: { $0.uuid == uuid }) else { return }
        manager.reportConversationEvent(.conversationEnded(.now, reason), for: conversation)
    }

    // MARK: Reports and the companion notification

    private func report(_ ring: UrgentCallRing, _ state: UrgentCallReport) {
        guard let api = AppModel.shared.api(ring.group)?.manager(ring.manager.isEmpty ? nil : ring.manager) else { return }
        Task {
            for attempt in 0..<3 {
                do {
                    try await api.reportUrgentCall(ring.id, state)
                    return
                } catch {
                    Self.log.notice("urgent call \(ring.id, privacy: .public): report \(state.rawValue, privacy: .public): \(error.localizedDescription, privacy: .public)")
                    try? await Task.sleep(for: .seconds(1 << attempt))
                }
            }
        }
    }

    /// A silent time-sensitive notification beside the call, which stays when the call's banner goes: "Persona is calling
    /// you", or after the ring "Missed urgent call".
    private func companion(_ ring: UrgentCallRing, missed: Bool, details: Bool) async {
        let content = UNMutableNotificationContent()
        content.title = missed ? String(localized: "Missed urgent call") : String(localized: "Persona is calling you")
        content.body = details ? ring.title : ""
        content.interruptionLevel = .timeSensitive
        content.sound = nil // the call rings; the notification only stays
        content.threadIdentifier = "urgent-call"
        content.userInfo = [Self.callKey: ring.id, PushCrypto.groupKey: ring.group]
        let request = UNNotificationRequest(identifier: Self.noteID(ring.id), content: content, trigger: nil)
        do {
            try await UNUserNotificationCenter.current().add(request)
        } catch {
            Self.log.notice("urgent call: companion notification: \(error.localizedDescription, privacy: .public)")
        }
    }

    private func removeCompanion(_ call: String) {
        let id = Self.noteID(call)
        UNUserNotificationCenter.current().removeDeliveredNotifications(withIdentifiers: [id])
        UNUserNotificationCenter.current().removePendingNotificationRequests(withIdentifiers: [id])
    }

    // MARK: LiveCommunicationKit

    /// The conversation manager's delegate: LiveCommunicationKit calls it on its own queue, so each call hops to the
    /// main actor.
    private final class Delegate: ConversationManagerDelegate, @unchecked Sendable {
        func conversationManager(_ manager: ConversationManager, conversationChanged conversation: Conversation) {}
        func conversationManagerDidBegin(_ manager: ConversationManager) {}
        func conversationManagerDidReset(_ manager: ConversationManager) {}

        func conversationManager(_ manager: ConversationManager, perform action: ConversationAction) {
            nonisolated(unsafe) let action = action
            let uuid = action.conversationUUID
            Task { @MainActor in
                switch action {
                case let join as JoinConversationAction:
                    join.fulfill(dateConnected: .now)
                    UrgentCallCenter.shared.answered(uuid)
                case let end as EndConversationAction:
                    end.fulfill(dateEnded: .now)
                    UrgentCallCenter.shared.endedOnPhone(uuid)
                default:
                    action.fail()
                }
            }
        }

        func conversationManager(_ manager: ConversationManager, timedOutPerforming action: ConversationAction) {}

        func conversationManager(_ manager: ConversationManager, didActivate audioSession: AVAudioSession) {
            Task { @MainActor in UrgentCallCenter.shared.audioActivated() }
        }

        func conversationManager(_ manager: ConversationManager, didDeactivate audioSession: AVAudioSession) {
            Task { @MainActor in UrgentCallCenter.shared.audioDeactivated() }
        }
    }
}
#endif
