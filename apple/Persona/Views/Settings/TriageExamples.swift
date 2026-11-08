import SwiftUI

/// Settings › Session Triage (#1413): what each switch makes of an agent, drawn with the Agents list's own row (`SlotRow`)
/// and Needs You card (`InteractionPreviewCard`) on made-up agents. Look only: never tapped, never sent anywhere.
enum TriageExample {
    /// The triage read the turn: the row says what the agent waits for.
    static func triaged(now: Date = Date()) -> Session? {
        session("triage", title: String(localized: "Fix the sign-in test"), now: now,
                extra: ["work": ["phase": "blocked", "on": "external", "line": String(localized: "Waiting for CI"),
                                 "wait": String(localized: "CI")]])
    }

    /// A decision the agent waits on, as its card in Needs You.
    static func decision(now: Date = Date()) -> Interaction {
        Interaction(id: "triage-example-decide", machineID: "example", sessionID: "decide",
                    sessionTitle: String(localized: "Release 2.4"), kind: .question, createdAt: now,
                    questions: [Question(question: String(localized: "Push the fix to beta?"), header: nil, multiSelect: false,
                                         options: [QuestionOption(label: String(localized: "Push")),
                                                   QuestionOption(label: String(localized: "Not Yet"))])])
    }

    /// A model error: the hub sends "continue" by itself, and the row says when.
    static func retrying(now: Date = Date()) -> Session? {
        session("continue", title: String(localized: "Refactor the sync code"), now: now,
                extra: ["work": ["phase": "blocked", "on": "transient", "line": String(localized: "Model service busy (503)"),
                                 "wake_at": stamp(now.addingTimeInterval(65)), "attempts": 1, "max": 3]])
    }

    /// Waiting on a deploy: the hub wakes it to check, and the row says when.
    static func waking(now: Date = Date()) -> Session? {
        session("wake", title: String(localized: "Ship the web build"), now: now,
                extra: ["work": ["phase": "blocked", "on": "external", "line": String(localized: "Waiting for the deploy"),
                                 "wake_at": stamp(now.addingTimeInterval(605))]])
    }

    /// Seen and not answered: listed as Awaiting Reply with its question, which the reminder is about.
    static func awaitingReply(now: Date = Date()) -> Session? {
        session("reply", title: String(localized: "Clean up old branches"), now: now,
                extra: ["awaits_reply": true, "seen_at": stamp(now.addingTimeInterval(-60)),
                        "asks": String(localized: "Delete these 12 merged branches?")])
    }

    private static func session(_ id: String, title: String, now: Date, extra: [String: Any]) -> Session? {
        var fields: [String: Any] = ["machine_id": "example", "session_id": id, "title": title, "cwd": "/example/app",
                                     "status": "idle", "state": "idle", "agent": "claude", "pending_interactions": 0,
                                     "updated_at": stamp(now.addingTimeInterval(-120))]
        fields.merge(extra) { $1 }
        guard let data = try? JSONSerialization.data(withJSONObject: fields) else { return nil }
        return try? JSON.decoder.decode(Session.self, from: data)
    }

    private static func stamp(_ date: Date) -> String { ISO8601DateFormatter().string(from: date) }
}

/// A switch's example under it: on a well, so it reads as a picture of the list rather than part of the page.
struct TriageExampleBox<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        content
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.s)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
            .allowsHitTesting(false)
            .accessibilityElement(children: .combine)
            .accessibilityHint(Text("Example"))
    }
}
