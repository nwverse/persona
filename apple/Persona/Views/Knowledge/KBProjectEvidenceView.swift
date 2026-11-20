import SwiftUI

/// A field can miss the same evidence as its neighbor. Keep the field names and counts together.
struct KBProjectReviewNote: View {
    var freshness: KBProjectFreshness?

    var body: some View {
        if let freshness, freshness.needsReview {
            let notes = ["summary", "one_line"].compactMap { key -> String? in
                guard let field = freshness.fields[key], field.needsReview else { return nil }
                let title = key == "summary" ? String(localized: "Summary needs review") : String(localized: "Overview needs review")
                guard let n = field.uncoveredEvidenceCount, n > 0 else { return title }
                return title + " · " + Self.newerUpdates(n)
            }
            Label {
                Text(notes.isEmpty ? String(localized: "Needs review") : notes.joined(separator: "\n"))
                    .fixedSize(horizontal: false, vertical: true)
            } icon: {
                ThemedIcon(Theme.Icon.time).foregroundStyle(Theme.Hue.orange)
            }
            .font(Theme.Font.caption).foregroundStyle(.secondary)
        }
    }
    static func newerUpdates(_ n: Int) -> String {
        n == 1 ? String(localized: "1 newer update") : String(localized: "\(n) newer updates")
    }
}

struct KBProjectEvidenceSection: View {
    var detail: KBDetail
    var api: APIClient
    var saved: () async -> Void
    @State private var expanded = false
    @State private var editing = false

    var body: some View {
        Section {
            Button { expanded.toggle() } label: {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
                    Label("Sources and coverage", systemImage: Theme.Icon.knowledge)
                    Spacer(minLength: Theme.Space.xs)
                    if detail.projectPolicy == .referenceOnly {
                        Text("References only").font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.caption).foregroundStyle(.secondary)
                        .rotationEffect(.degrees(expanded ? 90 : 0)).accessibilityHidden(true)
                }
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityValue(expanded ? Text("Expanded") : Text("Collapsed"))
            .accessibilityIdentifier("kb.project.evidence")

            if expanded {
                ForEach(["summary", "one_line"], id: \.self) { key in
                    if let freshness = detail.freshness, let field = freshness.fields[key] {
                        coverage(key == "summary" ? String(localized: "Summary") : String(localized: "One-line overview"), field, end: freshness.timelineEnd)
                    }
                }
                if let policy = detail.projectPolicy {
                    Button { editing = true } label: {
                        VStack(alignment: .leading, spacing: Theme.Space.xs) {
                            Label("Edit project storage", systemImage: Theme.Icon.edit)
                            Text(policy == .referenceOnly ? String(localized: "References only") : String(localized: "Managed by Persona"))
                                .font(Theme.Font.caption).foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityIdentifier("kb.project.edit")
                    .sheet(isPresented: $editing) {
                        if let draft = KBProjectStorageDraft(detail) {
                            KBProjectStorageEditor(draft: draft, id: detail.id, api: api, saved: saved)
                        }
                    }
                } else if let mode = detail.projectMode, !mode.isEmpty {
                    Text("Project mode: \(mode)").font(Theme.Font.caption).foregroundStyle(.secondary)
                }
            }
        }
        if expanded, let sources = detail.sources, !sources.isEmpty {
            Section {
                ForEach(sources) { source in
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        if let text = source.url, let url = KBProjectStorageDraft.webURL(text) {
                            Link(text, destination: url)
                            if source.id != text {
                                Text(source.id).font(Theme.Font.caption).foregroundStyle(.secondary).textSelection(.enabled)
                            }
                        } else {
                            Text(source.id).textSelection(.enabled)
                        }
                        if let state = source.deliveryState, !state.isEmpty {
                            Text("Source status: \(sourceStatus(state))").font(Theme.Font.caption).foregroundStyle(.secondary)
                        }
                        if let at = source.deliveryAt {
                            Text(at.formatted(date: .abbreviated, time: .shortened)).font(Theme.Font.label).foregroundStyle(.secondary)
                        }
                        if let verification = source.environmentVerification, !verification.isEmpty {
                            Text(verification == "unverified" ? String(localized: "Environment unverified") : String(localized: "Environment verification: \(verification)"))
                                .font(Theme.Font.caption).foregroundStyle(.secondary)
                        }
                    }
                    .padding(.vertical, Theme.Space.xxs)
                    ForEach(source.refs ?? [], id: \.self) { ref in sourceReference(ref) }
                }
            } header: {
                Text("Sources")
            } footer: {
                if sources.contains(where: { $0.deliveryState?.isEmpty == false }) {
                    Text("Deployment and acceptance need their own evidence.")
                }
            }
        }
    }

    private func coverage(_ title: String, _ field: KBProjectFreshness.Field, end: Int64) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(title).font(Theme.Font.secondaryStrong)
            if let reason = field.lockReason, !reason.isEmpty {
                Label(reason == "field_locked" ? String(localized: "Locked against automatic updates") : reason, systemImage: Theme.Icon.locked)
            }
            if let cutoff = field.cutoff {
                Text("Timeline bytes \(cutoff.timelineFrom)–\(cutoff.timelineThrough) of \(end)")
                if cutoff.timelineTruncated == true { Text("Earlier timeline omitted") }
            } else {
                Text("Coverage unknown")
            }
            if let n = field.uncoveredEvidenceCount {
                Text(KBProjectReviewNote.newerUpdates(n))
            } else if field.reviewReason != "reference-only" {
                Text("Number of newer updates unknown")
            }
            if field.reviewReason == "summary_edited" { Text("Summary changed") }
            if field.reviewReason == "evidence_changed" { Text("Source evidence changed") }
            Text(responsibility(field.writer, writer: true))
            Text(responsibility(field.reviewBy, writer: false))
            if let origin = field.source, !origin.isEmpty { Text("Receipt source: \(origin)") }
            if let refs = field.cutoff?.sources, !refs.isEmpty {
                Text("Based on: \(refs.map(\.ref).joined(separator: " · "))").textSelection(.enabled)
            }
        }
        .font(Theme.Font.caption).foregroundStyle(.secondary)
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
    }

    private func responsibility(_ value: String?, writer: Bool) -> String {
        switch value {
        case "user": return writer ? String(localized: "Written by you") : String(localized: "Maintained by you")
        case "persona": return writer ? String(localized: "Written by Persona") : String(localized: "Maintained by Persona")
        case let value? where value.hasPrefix("ext:") && value.count > 4:
            let name = String(value.dropFirst(4))
            return writer ? String(localized: "Written by \(name)") : String(localized: "Maintained by \(name)")
        default: return writer ? String(localized: "Writer unknown") : String(localized: "Maintainer unknown")
        }
    }

    private func sourceStatus(_ value: String) -> String {
        switch value {
        case "merged": String(localized: "Merged [Project source]")
        case "closed": String(localized: "Closed [Project source]")
        case "open": String(localized: "Open [Project source]")
        default: value
        }
    }

    @ViewBuilder private func sourceReference(_ ref: String) -> some View {
        let parts = ref.split(separator: "/", omittingEmptySubsequences: false)
        if let url = KBProjectStorageDraft.webURL(ref) {
            Link(ref, destination: url)
        } else if parts.count == 2, parts[0] == "moments", let moment = detail.moments?.first(where: { $0.id == parts[1] }) {
            NavigationLink { KBMomentView(ref: moment) } label: { Text(ref) }
        } else if parts.count == 2, !parts[1].isEmpty, KBKind(rawValue: String(parts[0])) != nil {
            let linked = detail.links?.first { $0.kind == parts[0] && $0.id == parts[1] }
            let entry = linked ?? KBEntry(kind: String(parts[0]), id: String(parts[1]), name: ref, relevance: 0)
            KBEntryLink(entry: entry) {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(entry.name)
                    if entry.name != ref { Text(ref).font(Theme.Font.caption).foregroundStyle(.secondary) }
                }
            }
        } else {
            Text(ref).textSelection(.enabled)
        }
    }
}

private struct KBProjectStorageEditor: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State var draft: KBProjectStorageDraft
    var id: String
    var api: APIClient
    var saved: () async -> Void
    @State private var saving = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Toggle("References only", isOn: Binding(get: { draft.policy == .referenceOnly }, set: {
                        draft.policy = $0 ? .referenceOnly : .full
                    })).accessibilityIdentifier("kb.project.reference-only")
                } footer: {
                    if draft.policy == .referenceOnly {
                        Text("Keep links to the originals. Existing notes stay here. New summaries, overviews and timeline entries are blocked.")
                    } else {
                        Text("Persona can update the overview, summary and timeline. Locked fields stay as written.")
                    }
                }
                Section {
                    TextField("Source references", text: $draft.references, axis: .vertical)
                        .lineLimit(2...6)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                        .accessibilityIdentifier("kb.project.pointers")
                } header: {
                    Text("Source references")
                } footer: {
                    if draft.validURLs {
                        Text("Enter one HTTP or HTTPS address per line.")
                    } else {
                        Text("Use up to 30 HTTP or HTTPS addresses without credentials, up to 2048 bytes each.")
                            .foregroundStyle(Theme.danger).accessibilityIdentifier("kb.project.invalid-urls")
                    }
                }
                if let error { Text(error).foregroundStyle(Theme.danger).accessibilityIdentifier("kb.project.error") }
            }
            .disabled(saving)
            .navigationTitle("Edit project storage")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                SheetClose(disabled: saving) { dismiss() }
                SheetConfirm(id: "kb.project.save", name: String(localized: "Save"),
                             disabled: draft.update == nil || !api.sameSettingsScope(as: model.api), busy: saving) { Task { await save() } }
            }
        }
        .interactiveDismissDisabled(saving)
        #if os(macOS)
        .macSheet()
        #endif
    }

    private func save() async {
        guard !saving, draft.update != nil, api.sameSettingsScope(as: model.api) else { return }
        saving = true
        defer { saving = false }
        do {
            try await api.saveProjectStorage(draft, id: id, current: model.api)
            guard api.sameSettingsScope(as: model.api) else { return }
            await saved()
            if api.sameSettingsScope(as: model.api) { dismiss() }
        } catch {
            guard api.sameSettingsScope(as: model.api) else { return }
            self.error = error.shown
        }
    }
}
