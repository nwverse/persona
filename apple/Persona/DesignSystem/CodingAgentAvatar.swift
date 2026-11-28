import SwiftUI

/// Primary contact identity for a coding Agent: resolves a model or Agent vendor brand mark (`Theme.Brand`)
/// and falls back gracefully to a single uppercase monogram letter when neither the harness nor model is known.
/// Pure data, testable without SwiftUI rendering and aligned with Web's `resolveCodingAgentAvatar`.
nonisolated struct CodingAgentIdentity: Equatable, Sendable {
    var brand: String?
    var name: String
    var letter: String
    var model: String?

    /// Product-branded harnesses whose own mark stays primary even when running another vendor's model.
    private static let productHarnesses: Set<String> = [
        Theme.Brand.agy,
        Theme.Brand.cursor,
        Theme.Brand.pi,
        Theme.Brand.trae,
        Theme.Brand.opencode,
        Theme.Brand.copilot,
        Theme.Brand.gemini,
        Theme.Brand.grok,
        Theme.Brand.kimi,
        Theme.Brand.dsh,
    ]

    init(brand: String?, name: String, model: String? = nil) {
        self.brand = brand
        self.name = name
        self.letter = Self.firstLetter(of: name)
        self.model = model
    }

    init(_ session: Session) {
        let kind = AgentKind(session)
        let rawAgent = (session.agent ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let trimmedModel = session.model?.trimmingCharacters(in: .whitespacesAndNewlines)
        let modelValue = trimmedModel?.isEmpty == false ? trimmedModel : nil
        self.model = modelValue

        if let brand = kind.brand, Self.productHarnesses.contains(brand) {
            let displayName = Self.preferredProductName(brand: brand, rawName: kind.name, explicitHint: session.agentLabelHint)
            self.brand = brand
            self.name = displayName
            self.letter = Self.firstLetter(of: displayName)
            return
        }

        if let vendor = Self.modelVendor(named: modelValue) {
            let sameVendor = kind.brand == vendor.brand
            let displayName = sameVendor ? kind.name : vendor.name
            self.brand = vendor.brand
            self.name = displayName
            self.letter = Self.firstLetter(of: displayName)
            return
        }

        if let brand = kind.brand, rawAgent != MachineEngine.paseo.rawValue || AgentKind.brand(named: session.launcher) != nil {
            self.brand = brand
            self.name = kind.name
            self.letter = Self.firstLetter(of: kind.name)
            return
        }

        let fallbackName = kind.name.trimmingCharacters(in: .whitespacesAndNewlines)
        let letterSource = !fallbackName.isEmpty ? fallbackName
            : (session.launcher?.trimmingCharacters(in: .whitespacesAndNewlines) ?? modelValue ?? "?")
        self.brand = nil
        self.name = fallbackName.isEmpty ? "Agent" : fallbackName
        self.letter = Self.firstLetter(of: letterSource)
    }

    /// Maps a model ID (including vendor-prefixed gateway IDs like `google-antigravity/gemini-4-argon:high`
    /// or `anthropic/claude-sonnet-4-6[1m]`) to a bundled vendor mark when available.
    static func modelVendor(named model: String?) -> (brand: String, name: String)? {
        guard let raw = model?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(), !raw.isEmpty else {
            return nil
        }
        let withoutContext: String
        if let bracket = raw.firstIndex(of: "["), raw.hasSuffix("]") {
            withoutContext = String(raw[..<bracket])
        } else {
            withoutContext = raw
        }
        let parts = withoutContext.split(separator: "/").map(String.init)
        let last = parts.last ?? withoutContext
        let stem = String(last.split(separator: ":", maxSplits: 1).first ?? Substring(last))
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let prefix = parts.count > 1 ? parts.dropLast().joined(separator: "/") : ""

        if stem.hasPrefix("claude") || prefix.contains("anthropic") {
            return (Theme.Brand.claude, "Claude")
        }
        if stem.hasPrefix("gpt") || stem.hasPrefix("chatgpt") || stem.hasPrefix("codex")
            || Self.isOpenAIReasoningModel(stem) || prefix.contains("openai") {
            return (Theme.Brand.openAI, "OpenAI")
        }
        if stem.hasPrefix("gemini") || prefix.contains("gemini") {
            return (Theme.Brand.gemini, "Gemini")
        }
        if stem.hasPrefix("deepseek") || prefix.contains("deepseek") {
            return (Theme.Brand.dsh, "DeepSeek")
        }
        if stem.hasPrefix("kimi") || prefix.contains("moonshot") || prefix.contains("kimi") {
            return (Theme.Brand.kimi, "Kimi")
        }
        if stem.hasPrefix("grok") || prefix.contains("xai") || prefix.contains("grok") {
            return (Theme.Brand.grok, "Grok")
        }
        return nil
    }

    private static func preferredProductName(brand: String, rawName: String, explicitHint: String?) -> String {
        if let hint = explicitHint?.trimmingCharacters(in: .whitespacesAndNewlines), !hint.isEmpty {
            return hint
        }
        switch brand {
        case Theme.Brand.agy: return "Antigravity CLI"
        case Theme.Brand.cursor: return "Cursor"
        case Theme.Brand.gemini: return "Gemini CLI"
        case Theme.Brand.grok: return "Grok"
        case Theme.Brand.kimi: return "Kimi"
        case Theme.Brand.dsh: return "DSH"
        case Theme.Brand.copilot: return "GitHub Copilot"
        case Theme.Brand.opencode: return "opencode"
        case Theme.Brand.pi: return "pi"
        default: return rawName
        }
    }

    private static func isOpenAIReasoningModel(_ stem: String) -> Bool {
        for p in ["o1", "o3", "o4"] where stem == p || stem.hasPrefix(p + "-") || stem.hasPrefix(p + ".") {
            return true
        }
        return false
    }

    private static func firstLetter(of text: String) -> String {
        text.first(where: { $0.isLetter || $0.isNumber }).map { String($0).uppercased() } ?? "?"
    }
}

/// Hosting software / engine that owns a coding Agent session (`orca`, `herdr`, `herdr-core`, `paseo`,
/// `t3code`, `cindy`, `tern`, `rex`, `botmux`, or a future custom host).
nonisolated enum HostingEngine: Equatable, Hashable, Sendable {
    case orca
    case herdr
    case herdrCore
    case paseo
    case t3code
    case cindy
    case tern
    case rex
    case botmux
    case custom(String)

    static let allKnown: [HostingEngine] = [
        .orca, .herdr, .herdrCore, .paseo, .t3code, .cindy, .tern, .rex, .botmux,
    ]

    init?(id raw: String?) {
        guard let trimmed = raw?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(), !trimmed.isEmpty else {
            return nil
        }
        switch trimmed {
        case "orca": self = .orca
        case "herdr": self = .herdr
        case "herdr-core": self = .herdrCore
        case "paseo": self = .paseo
        case "t3code": self = .t3code
        case "cindy": self = .cindy
        case "tern": self = .tern
        case "rex": self = .rex
        case "botmux": self = .botmux
        default: self = .custom(trimmed)
        }
    }

    /// Resolves the hosting engine from a session's `host`, known `spaceKind`, `paneID` prefix (`hostKind`),
    /// or runtime `sourceInstanceID`. Returns nil for unhosted/standalone sessions.
    static func of(_ session: Session) -> HostingEngine? {
        if let explicit = HostingEngine(id: session.host) {
            return explicit
        }
        if let space = HostingEngine(id: session.spaceKind), !space.isCustom {
            return space
        }
        if let paneHost = HostingEngine(id: session.hostKind) {
            return paneHost
        }
        if session.sourceKind == "runtime",
           let runtimeHost = HostingEngine(id: session.sourceInstanceID),
           !runtimeHost.isCustom {
            return runtimeHost
        }
        return nil
    }

    var isCustom: Bool {
        if case .custom = self { return true }
        return false
    }

    var id: String {
        switch self {
        case .orca: "orca"
        case .herdr: "herdr"
        case .herdrCore: "herdr-core"
        case .paseo: "paseo"
        case .t3code: "t3code"
        case .cindy: "cindy"
        case .tern: "tern"
        case .rex: "rex"
        case .botmux: "botmux"
        case .custom(let raw): raw
        }
    }

    var displayName: String {
        switch self {
        case .orca: "Orca"
        case .herdr: "herdr"
        case .herdrCore: "Herdr App"
        case .paseo: "Paseo"
        case .t3code: "T3 Code"
        case .cindy: "Cindy"
        case .tern: "Tern"
        case .rex: "Rex"
        case .botmux: "botmux"
        case .custom(let raw): raw
        }
    }

    var letter: String {
        displayName.first(where: { $0.isLetter || $0.isNumber }).map { String($0).uppercased() } ?? "?"
    }

    var badgeColor: Color {
        switch self {
        case .orca: EngineBadgeTokens.orca
        case .herdr: EngineBadgeTokens.herdr
        case .herdrCore: EngineBadgeTokens.herdrCore
        case .paseo: EngineBadgeTokens.paseo
        case .t3code: EngineBadgeTokens.t3code
        case .cindy: EngineBadgeTokens.cindy
        case .tern: EngineBadgeTokens.tern
        case .rex: EngineBadgeTokens.rex
        case .botmux: EngineBadgeTokens.botmux
        case .custom: EngineBadgeTokens.custom
        }
    }
}

/// Pure view model for `CodingAgentAvatar`, combining the model/vendor identity and optional hosting engine.
nonisolated struct CodingAgentAvatarModel: Equatable, Sendable {
    var identity: CodingAgentIdentity
    var engine: HostingEngine?

    init(identity: CodingAgentIdentity, engine: HostingEngine?) {
        self.identity = identity
        self.engine = engine
    }

    init(_ session: Session, showEngineBadge: Bool = true) {
        self.identity = CodingAgentIdentity(session)
        self.engine = showEngineBadge ? HostingEngine.of(session) : nil
    }

    /// Accessible name combining model/Agent and hosting engine (e.g. "Claude Code · Orca").
    var spokenLabel: String {
        if let engine {
            return "\(identity.name) · \(engine.displayName)"
        }
        return identity.name
    }
}

/// Reusable contact-style circular avatar for a coding Agent:
/// - Main circular disc: model or Agent vendor mark (or uppercase single-letter fallback).
/// - Bottom-trailing corner: compact circular outlined hosting-engine badge (`HostingEngineBadge`).
/// - Top-trailing corner: optional live `StatusDot`, positioned opposite the engine badge so neither overlaps.
struct CodingAgentAvatar: View {
    struct Status: Equatable {
        var state: AgentState
        var size: CGFloat
        var waiting: Bool
        var justFinished: Bool
    }

    var model: CodingAgentAvatarModel
    var side: CGFloat = Theme.Size.avatar
    var ringColor: Color = Theme.card
    var status: Status? = nil
    var decorative = false

    init(session: Session, side: CGFloat = Theme.Size.avatar, ringColor: Color = Theme.card, status: Status? = nil, showEngineBadge: Bool = true, decorative: Bool = false) {
        self.model = CodingAgentAvatarModel(session, showEngineBadge: showEngineBadge)
        self.side = side
        self.ringColor = ringColor
        self.status = status
        self.decorative = decorative
    }

    init(model: CodingAgentAvatarModel, side: CGFloat = Theme.Size.avatar, ringColor: Color = Theme.card, status: Status? = nil, decorative: Bool = false) {
        self.model = model
        self.side = side
        self.ringColor = ringColor
        self.status = status
        self.decorative = decorative
    }

    private var badgeSide: CGFloat {
        min(EngineBadgeTokens.badgeMaxSide, max(EngineBadgeTokens.badgeMinSide, side * EngineBadgeTokens.badgeRatio))
    }

    var body: some View {
        ZStack {
            if let brand = model.identity.brand {
                BrandMark(brand, side: side * EngineBadgeTokens.markRatio)
                    .foregroundStyle(.primary)
            } else {
                Text(model.identity.letter)
                    .font(side >= Theme.Size.avatar ? Theme.Font.secondaryStrong : Theme.Font.labelHeavy)
                    .minimumScaleFactor(0.5)
                    .lineLimit(1)
                    .foregroundStyle(.secondary)
                    .dynamicTypeSize(...DynamicTypeSize.xLarge)
            }
        }
        .scaledFrame(width: side, height: side)
        .background(Theme.Fill.well, in: Circle())
        .overlay(Circle().stroke(Theme.cardStroke, lineWidth: Theme.Size.hairline))
        .overlay(alignment: .topTrailing) {
            if let status {
                StatusDot(state: status.state, size: status.size, waiting: status.waiting, justFinished: status.justFinished)
                    .padding(Theme.Size.rule)
                    .background(ringColor, in: Circle())
                    .offset(x: EngineBadgeTokens.cornerOffset, y: -EngineBadgeTokens.cornerOffset)
            }
        }
        .overlay(alignment: .bottomTrailing) {
            if let engine = model.engine {
                HostingEngineBadge(engine: engine, side: badgeSide, ringColor: ringColor)
                    .offset(x: EngineBadgeTokens.cornerOffset, y: EngineBadgeTokens.cornerOffset)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(model.spokenLabel)
        .accessibilityHidden(decorative)
        .accessibilityIdentifier("agent.mark")
    }
}

/// Compact circular outlined badge logo for a coding Agent's hosting engine.
struct HostingEngineBadge: View {
    var engine: HostingEngine
    var side: CGFloat = Theme.Size.markM
    var ringColor: Color = Theme.card

    var body: some View {
        ZStack {
            Circle().fill(engine.badgeColor)
            engineGlyph
                .scaledFrame(width: side * EngineBadgeTokens.glyphRatio, height: side * EngineBadgeTokens.glyphRatio)
        }
        .scaledFrame(width: side, height: side)
        .overlay(Circle().strokeBorder(ringColor, lineWidth: Theme.Size.lineMedium))
        .accessibilityHidden(true)
    }

    @ViewBuilder private var engineGlyph: some View {
        switch engine {
        case .orca:
            OrcaFinGlyph().foregroundStyle(Theme.onSolid)
        case .herdr:
            HerdrGlyph().stroke(Theme.onSolid, style: strokeStyle)
        case .herdrCore:
            HerdrAppGlyph().stroke(Theme.onSolid, style: strokeStyle)
        case .paseo:
            PaseoGlyph().stroke(Theme.onSolid, style: strokeStyle)
        case .t3code:
            T3CodeGlyph().stroke(Theme.onSolid, style: strokeStyle)
        case .cindy:
            CindyGlyph().foregroundStyle(Theme.onSolid)
        case .tern:
            TernGlyph().stroke(Theme.onSolid, style: strokeStyle)
        case .rex:
            RexGlyph().stroke(Theme.onSolid, style: strokeStyle)
        case .botmux:
            BotmuxGlyph().foregroundStyle(Theme.onSolid)
        case .custom:
            Text(engine.letter)
                .font(Theme.Font.labelHeavy)
                .minimumScaleFactor(0.4)
                .lineLimit(1)
                .foregroundStyle(Theme.onSolid)
                .dynamicTypeSize(...DynamicTypeSize.large)
        }
    }

    private var strokeStyle: StrokeStyle {
        StrokeStyle(
            lineWidth: max(Theme.Size.line, side * EngineBadgeTokens.glyphRatio * EngineBadgeTokens.strokeFactor),
            lineCap: .round,
            lineJoin: .round
        )
    }
}

// MARK: - Hosting engine vector glyphs (16x16 normalized coordinates matching Web's CodingAgentAvatar)

private struct OrcaFinGlyph: View {
    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            let h = geo.size.height
            let s = min(w, h) / 16
            ZStack {
                Path { p in
                    p.move(to: CGPoint(x: 3.2 * s, y: 11.4 * s))
                    p.addCurve(to: CGPoint(x: 11.8 * s, y: 3.2 * s),
                               control1: CGPoint(x: 4.2 * s, y: 6.8 * s),
                               control2: CGPoint(x: 7.5 * s, y: 3.6 * s))
                    p.addCurve(to: CGPoint(x: 12.2 * s, y: 9.8 * s),
                               control1: CGPoint(x: 10.8 * s, y: 5.4 * s),
                               control2: CGPoint(x: 10.7 * s, y: 7.6 * s))
                    p.closeSubpath()
                }.fill()
                Path { p in
                    p.move(to: CGPoint(x: 2.2 * s, y: 12.4 * s))
                    p.addQuadCurve(to: CGPoint(x: 8.1 * s, y: 12.4 * s), control: CGPoint(x: 5.1 * s, y: 11.1 * s))
                    p.addQuadCurve(to: CGPoint(x: 13.8 * s, y: 12.4 * s), control: CGPoint(x: 11.0 * s, y: 13.6 * s))
                }.stroke(style: StrokeStyle(lineWidth: max(Theme.Size.line, 1.7 * s), lineCap: .round))
            }
        }
    }
}

private struct HerdrGlyph: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.move(to: CGPoint(x: 4.2 * s, y: 3.2 * s))
            p.addLine(to: CGPoint(x: 4.2 * s, y: 12.8 * s))
            p.move(to: CGPoint(x: 11.8 * s, y: 3.2 * s))
            p.addLine(to: CGPoint(x: 11.8 * s, y: 12.8 * s))
            p.move(to: CGPoint(x: 4.2 * s, y: 8.0 * s))
            p.addLine(to: CGPoint(x: 11.8 * s, y: 8.0 * s))
        }
    }
}

private struct HerdrAppGlyph: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.addRoundedRect(in: CGRect(x: 2.6 * s, y: 3.2 * s, width: 10.8 * s, height: 9.6 * s),
                             cornerSize: CGSize(width: 2.2 * s, height: 2.2 * s))
            p.move(to: CGPoint(x: 7.6 * s, y: 3.5 * s))
            p.addLine(to: CGPoint(x: 7.6 * s, y: 12.5 * s))
        }
    }
}

private struct PaseoGlyph: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.move(to: CGPoint(x: 5.4 * s, y: 12.4 * s))
            p.addLine(to: CGPoint(x: 5.4 * s, y: 3.6 * s))
            p.addLine(to: CGPoint(x: 8.8 * s, y: 3.6 * s))
            p.addArc(center: CGPoint(x: 8.8 * s, y: 6.25 * s), radius: 2.65 * s,
                     startAngle: .degrees(-90), endAngle: .degrees(90), clockwise: false)
            p.addLine(to: CGPoint(x: 5.4 * s, y: 8.9 * s))
        }
    }
}

private struct T3CodeGlyph: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.move(to: CGPoint(x: 2.4 * s, y: 4.5 * s))
            p.addLine(to: CGPoint(x: 7.2 * s, y: 4.5 * s))
            p.move(to: CGPoint(x: 4.8 * s, y: 4.5 * s))
            p.addLine(to: CGPoint(x: 4.8 * s, y: 11.7 * s))
            p.move(to: CGPoint(x: 9.2 * s, y: 4.6 * s))
            p.addLine(to: CGPoint(x: 12.6 * s, y: 4.6 * s))
            p.addLine(to: CGPoint(x: 10.7 * s, y: 7.4 * s))
            p.addLine(to: CGPoint(x: 11.4 * s, y: 7.4 * s))
            p.addArc(center: CGPoint(x: 11.4 * s, y: 9.3 * s), radius: 1.9 * s,
                     startAngle: .degrees(-90), endAngle: .degrees(90), clockwise: false)
            p.addLine(to: CGPoint(x: 9.1 * s, y: 11.2 * s))
        }
    }
}

private struct CindyGlyph: View {
    var body: some View {
        ZStack {
            CindyArcShape().stroke(style: StrokeStyle(lineWidth: Theme.Size.lineMedium, lineCap: .round))
            CindyDotShape().fill()
        }
    }
}

private struct CindyArcShape: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.addArc(center: CGPoint(x: 8 * s, y: 8 * s), radius: 4.4 * s,
                     startAngle: .degrees(45), endAngle: .degrees(315), clockwise: false)
        }
    }
}

private struct CindyDotShape: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        let r = 1.4 * s
        return Path { p in
            p.addEllipse(in: CGRect(x: 11.9 * s - r, y: 8 * s - r, width: r * 2, height: r * 2))
        }
    }
}

private struct TernGlyph: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.move(to: CGPoint(x: 2.5 * s, y: 5.4 * s))
            p.addLine(to: CGPoint(x: 8.0 * s, y: 10.6 * s))
            p.addLine(to: CGPoint(x: 13.5 * s, y: 5.4 * s))
        }
    }
}

private struct RexGlyph: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.move(to: CGPoint(x: 2.8 * s, y: 11.4 * s))
            p.addLine(to: CGPoint(x: 4.2 * s, y: 5.2 * s))
            p.addLine(to: CGPoint(x: 8.0 * s, y: 8.2 * s))
            p.addLine(to: CGPoint(x: 11.8 * s, y: 5.2 * s))
            p.addLine(to: CGPoint(x: 13.2 * s, y: 11.4 * s))
            p.closeSubpath()
        }
    }
}

private struct BotmuxGlyph: View {
    var body: some View {
        ZStack {
            BotmuxLinksShape().stroke(style: StrokeStyle(lineWidth: Theme.Size.lineMedium, lineCap: .round, lineJoin: .round))
            BotmuxNodesShape().fill()
        }
    }
}

private struct BotmuxLinksShape: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        return Path { p in
            p.move(to: CGPoint(x: 5.0 * s, y: 5.2 * s))
            p.addLine(to: CGPoint(x: 11.0 * s, y: 8.0 * s))
            p.addLine(to: CGPoint(x: 5.0 * s, y: 10.8 * s))
        }
    }
}

private struct BotmuxNodesShape: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 16
        let rSmall = 1.5 * s
        let rLarge = 1.7 * s
        return Path { p in
            p.addEllipse(in: CGRect(x: 4.6 * s - rSmall, y: 5.2 * s - rSmall, width: rSmall * 2, height: rSmall * 2))
            p.addEllipse(in: CGRect(x: 4.6 * s - rSmall, y: 10.8 * s - rSmall, width: rSmall * 2, height: rSmall * 2))
            p.addEllipse(in: CGRect(x: 11.4 * s - rLarge, y: 8.0 * s - rLarge, width: rLarge * 2, height: rLarge * 2))
        }
    }
}
