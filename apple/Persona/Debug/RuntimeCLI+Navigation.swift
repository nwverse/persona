#if DEBUG && (os(iOS) || os(visionOS))
import SwiftUI
import UIKit
#if os(iOS)
import ActivityKit
#endif

/// Moving the app (docs/operations/runtime-cli.md「Navigation and styles」): `reset`, `navigate`, `set-style`, `set-appearance`, `defaults`.
extension RuntimeCLI {
    static let navigationCommands: [String: Command] = [
        "reset": { a in await reset(a) },
        "navigate": { a in await navigate(a) },
        "set-style": { a in setStyle(a) },
        "set-appearance": { a in setAppearance(a) },
        "defaults": { a in defaults(a) },
        "native-upgrade-proof": { a in nativeUpgradeProof(a) },
    ]

    /// Read-only local upgrade evidence; no credentials, writes, network calls or queue replay.
    private static func nativeUpgradeProof(_ args: Args) -> Reply {
        let model = AppModel.shared
        let recordings = RecordingLibrary.shared.entries
        var reply: Reply = ["chosenPersona": model.personas.chosenID, "displayedPersona": model.personas.current.id,
                            "outboxCount": model.outboxCount, "recordingCount": recordings.count,
                            "undeliveredSegments": recordings.reduce(0) { $0 + $1.segments.filter { !$0.delivered }.count },
                            "pinnedAgents": PersonalAgentPrefsStore.shared.prefs.pins]
        if let chat = args["draftChat"] as? String { reply["draftCharacters"] = LarkDrafts.shared[.chat(chat)].text.count }
        if args["includePaths"] as? Bool == true { reply["recordingRoot"] = RecordingLibrary.directory.path }
        #if os(iOS)
        reply["legacyActivityIDs"] = Activity<LegacyFleetActivityAttributes>.activities.map(\.id)
        reply["legacyActivityWorkingCounts"] = Dictionary(uniqueKeysWithValues: Activity<LegacyFleetActivityAttributes>.activities.map {
            ($0.id, $0.content.state.working)
        })
        reply["canonicalActivityIDs"] = Activity<PersonaActivityAttributes>.activities.map(\.id)
        #endif
        return reply
    }

    /// "machine/session", or "group|machine/session" for another group of hubs.
    static func key(_ s: String?) -> SessionKey? {
        guard let s else { return nil }
        return AppModel.shared.resolve(path: s)
    }


    /// A new view tree: the window's root becomes a new hosting controller for `RootView`, so every page's own state
    /// (the Agents page's pushed chat, the drawer, scroll positions, the glass bars) starts over. `route` runs between
    /// the old tree and the new one: what is set there is what the new pages find when they are built, as with the
    /// launch switches, and the old pages, gone by then, cannot take it first (they consumed `openChat`).
    static func rebuild(route: () -> Void) async {
        let m = AppModel.shared
        guard let w = window else { return }
        w.rootViewController = UIViewController()
        try? await Task.sleep(for: .milliseconds(100))
        m.shellSettings = false
        m.settingsPath = NavigationPath()
        m.openChat = nil
        AppModel.rememberChat(nil)
        route()
        // What WindowGroup gives its content and a bare hosting controller does not: without the scene's phase every
        // page that only works while the app is active (`.tabActiveTask`, machines' metrics) would wait forever.
        let phase: ScenePhase = UIApplication.shared.applicationState == .active ? .active : .inactive
        w.rootViewController = UIHostingController(rootView: RootView().environment(m).environment(\.scenePhase, phase))
        w.makeKeyAndVisible()
        try? await Task.sleep(for: .milliseconds(50))
    }

    /// A new view tree as after a launch (`rebuild`), the route left as it is. `coldChats`: no chat kept in memory
    /// either, so each loads again (from the disk cache, then the hub) when its page comes up, as after a launch;
    /// `dropChatCache`: none on disk either (`-openChatsFresh YES`).
    private static func reset(_ a: Args) async -> Reply {
        await rebuild { cool(a) }
        return state()
    }

    /// Runs between the old tree and the new one (`rebuild`'s route): only then are the old pages' chats no longer
    /// shown or subscribed, which AppModel's eviction would keep.
    private static func cool(_ a: Args) {
        if a["dropChatCache"] as? Bool == true { ChatCache.removeAll() }
        guard a["coldChats"] as? Bool == true else { return }
        let m = AppModel.shared
        // Pushed out through AppModel's own eviction: it keeps the last `keptChats` idle chats.
        for i in 0..<(AppModel.keptChats + 2) { _ = m.chat(SessionKey(machineID: "runtime-cli", sessionID: "evict-\(i)")) }
    }

    /// The page a launch switch opens (PersonaApp, SettingsView.openDebugPage), on a new view tree (`rebuild`), or in
    /// place with `keep`. `defaults`: launch-only switches the pages read when they are built (`openAllWork`…),
    /// registered for this run, never saved. `coldChats`, `dropChatCache`: as `reset`.
    private static func navigate(_ a: Args) async -> Reply {
        let m = AppModel.shared
        let scene = a["scene"] as? String ?? ""
        var tab = AppTab.assistant
        var chat: SessionKey?
        var assistant: SessionKey?? = .none
        var path = NavigationPath()
        switch scene {
        case "home": assistant = .some(nil)
        case "assistant": assistant = .some(key(a["session"] as? String))
        case "agents": tab = .chats
        case "chat":
            tab = .chats
            guard let k = key(a["session"] as? String) else { return fail("chat needs session <machine>/<id>") }
            chat = k
        case "inbox": tab = .mail
        case "settings": tab = .settings
        case "machines":
            tab = .settings
            path.append(MachineListRoute())
            if let id = a["machine"] as? String { path.append(MachineDetailRoute(id: id)) }
        default: return fail("unknown scene \(scene) (home|assistant|agents|chat|inbox|settings|machines)")
        }
        if let flags = a["defaults"] as? Args { UserDefaults.standard.register(defaults: flags) }
        let route = {
            cool(a)
            m.selectedTab = tab
            if case .some(let k) = assistant { m.assistantCurrent = k }
            m.openChat = chat
            m.settingsPath = path
        }
        if a["keep"] as? Bool == true { route() } else { await rebuild(route: route) }
        // A launch loads the chat's first page over HTTP (its socket is not up yet: AppModel.subscribe); a connected
        // app only gets the subscription's replay, the last exchanges. Cold, the page is loaded as after a launch.
        if a["coldChats"] as? Bool == true, let k = chat ?? assistant.flatMap({ $0 }) { await m.loadInitial(k) }
        return state()
    }

    private static func setStyle(_ a: Args) -> Reply {
        let m = AppModel.shared
        if let s = a["style"] as? String {
            guard let style = AppStyle(rawValue: s) else { return fail("unknown style \(s): \(AppStyle.allCases.map(\.rawValue))") }
            m.style = style
        }
        if let c = a["color"] as? String {
            guard let color = ThemeColor(rawValue: c) else { return fail("unknown color \(c): \(ThemeColor.allCases.map(\.rawValue))") }
            m.themeColor = color
        }
        return state()
    }

    /// The app's windows only (`simctl ui appearance` changes the whole simulator, status bar included).
    private static func setAppearance(_ a: Args) -> Reply {
        // Through Settings › Appearance › Light & Dark (one source of truth: the page shows it, a new scene keeps it); saved like a tap.
        AppModel.shared.colorScheme = ColorSchemeChoice(rawValue: a["value"] as? String ?? "") ?? .system
        return state()
    }

    /// `set`: values in the registration domain (this run only, never saved); `remove`: saved keys deleted (what
    /// `-openAgentsFresh YES` forgets); `get`: what they read now.
    private static func defaults(_ a: Args) -> Reply {
        let d = UserDefaults.standard
        if let set = a["set"] as? Args { d.register(defaults: set) }
        for k in a["remove"] as? [String] ?? [] { d.removeObject(forKey: k) }
        var r: Args = [:]
        for k in a["get"] as? [String] ?? [] { r[k] = d.object(forKey: k) ?? NSNull() }
        return ["values": r]
    }
}
#endif
