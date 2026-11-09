import SwiftUI

/// Settings › Appearance › Avatar (docs/apple/persona.md): Mochi and the hub's Milk Frog package, in a
/// grid like the styles, and the chosen one's top-bar dot in each state under it. Choosing one changes Persona's name, face and dot; the app's
/// colors only under Theme Color › Match Avatar (`ThemeColorPicker`). The tree is rebuilt, as for a style; Appearance stays open because
/// the settings path is AppModel's. A package is downloaded first and kept on the phone.
/// With a hub that makes avatars from images (`look_uploads`, #2405), New from Photo ends the grid (`AvatarCropView`), and an
/// avatar made that way can be deleted from its card's menu.
struct PersonaPicker: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var uploads = AvatarUploadsModel()
    /// The crop sheet for the picked image.
    @State private var cropping: AvatarUploadModel?
    @State private var choosingPhoto = false
    @State private var choosingFile = false
    /// The uploaded avatar whose deletion is being confirmed.
    @State private var confirmingDelete: Persona?

    private var store: PersonaStore { model.personas }
    /// A card's picture area: the 64 pt face with room around it.
    private static let cardHeight: CGFloat = PersonaStore.thumbnailPoints + Theme.Space.xxl
    private var columns: [GridItem] {
        let n = typeSize.isAccessibilitySize ? 2 : 3
        let item = GridItem(.flexible(), spacing: 12, alignment: .top)
        return Array(repeating: item, count: n)
    }

    var body: some View {
        grid
            .padding(.horizontal, Theme.Space.l)
            .task { await store.refresh(model.api) }
            .modifier(AvatarSources(uploads: uploads, choosingPhoto: $choosingPhoto, choosingFile: $choosingFile, cropping: $cropping))
            #if DEBUG
            .task { await debugOpen() }
            #endif
    }

    private var grid: some View {
        VStack(alignment: .leading, spacing: Theme.Space.l) {
            LazyVGrid(columns: columns, alignment: .leading, spacing: 14) {
                ForEach(model.isDemo ? [Persona.builtin] : store.offered) { p in
                    card(p)
                }
                if uploads.available { newCard }
            }
            states
            if let error = store.error, !model.isDemo {
                Label(error, systemImage: Theme.Icon.error)
                    .font(Theme.Font.caption)
                    .foregroundStyle(Theme.cautionText)
            }
            if let error = uploads.error, !model.isDemo {
                Label(error, systemImage: Theme.Icon.error)
                    .font(Theme.Font.caption)
                    .foregroundStyle(Theme.cautionText)
                    .accessibilityIdentifier("persona-upload-error")
            }
        }
    }

    /// New from Photo: after the avatars, a card with nothing in it yet. The phone opens Photos (Files from the card's
    /// menu); the Mac opens an image file, which its open panel also finds in Photos (Photos itself from the menu).
    private var newCard: some View {
        Button {
            #if os(macOS)
            choosingFile = true
            #else
            choosingPhoto = true
            #endif
        } label: {
            VStack(spacing: Theme.Space.s) {
                RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                    .fill(Theme.Fill.well)
                    .overlay {
                        if uploads.loading {
                            ProgressView()
                        } else {
                            ThemedIcon(Theme.Icon.newAvatar).font(Theme.Font.title).foregroundStyle(Theme.accent)
                        }
                    }
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                        .strokeBorder(Theme.AddCard.edge, style: StrokeStyle(lineWidth: Theme.Size.lineMedium, dash: Theme.AddCard.dash)))
                    .frame(maxWidth: .infinity)
                    .frame(height: Self.cardHeight)
                VStack(spacing: Theme.Space.xxs) {
                    Text("New from Photo").font(Theme.Font.captionStrong).lineLimit(1).minimumScaleFactor(0.8)
                    Text("Crop and upload").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).minimumScaleFactor(0.8)
                }
            }
            .frame(maxWidth: .infinity)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .disabled(uploads.loading)
        .contextMenu {
            Button { choosingPhoto = true } label: { Label(String(localized: "Choose from Photos"), systemImage: Theme.Icon.photos) }
            Button { choosingFile = true } label: { Label(String(localized: "Choose Image File"), systemImage: Theme.Icon.file) }
        }
        .accessibilityLabel(String(localized: "New from Photo"))
        .accessibilityHint(String(localized: "Makes an avatar from an image of your own"))
        .accessibilityIdentifier("persona-new")
    }

    #if DEBUG
    /// Opened once a launch: `-showAvatarCrop <image path>` (with `-showAvatarCropName <name>`) the crop sheet,
    /// `-showAvatarDelete <id>` the question before deleting that uploaded avatar (once it is listed).
    private static var debugOpened = false

    private func debugOpen() async {
        guard !Self.debugOpened else { return }
        let d = UserDefaults.standard
        if let path = d.string(forKey: "showAvatarCrop"), let data = FileManager.default.contents(atPath: path) {
            Self.debugOpened = true
            try? await Task.sleep(for: .seconds(1))
            guard let sheet = await uploads.picked(data) else { return }
            sheet.name = d.string(forKey: "showAvatarCropName") ?? ""
            cropping = sheet
        } else if let id = d.string(forKey: "showAvatarDelete") {
            Self.debugOpened = true
            for _ in 0..<20 {
                if let p = store.offered.first(where: { $0.id == id }) {
                    try? await Task.sleep(for: .seconds(1))
                    confirmingDelete = p
                    return
                }
                try? await Task.sleep(for: .milliseconds(500))
            }
        }
    }
    #endif

    /// The chosen one's dot in each state, moving as in the top bar: in a row; at the accessibility text sizes two a row,
    /// where five in a row cut every name to "…" and three ran them together (#1796).
    private var states: some View {
        Group {
            if typeSize.isAccessibilitySize {
                LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: Theme.Space.xxs, alignment: .top), count: 2),
                          spacing: Theme.Space.s) { stateCells }
            } else {
                HStack(spacing: Theme.Space.xxs) { stateCells }
            }
        }
        .padding(.vertical, Theme.Space.s)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(model.persona.name)'s five states in the top bar: idle, thinking, working, waiting for you, offline")
        .accessibilityIdentifier("persona-states")
    }

    @ViewBuilder private var stateCells: some View {
        ForEach(PersonaState.allCases) { s in
            VStack(spacing: Theme.Space.xs) {
                PersonaDot(size: Theme.Size.avatar, state: s)
                Text(s.title).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).minimumScaleFactor(0.8)
            }
            .frame(maxWidth: .infinity)
        }
    }

    private func card(_ p: Persona) -> some View {
        let selected = p.id == model.persona.id
        let here = store.isOnPhone(p)
        return Button {
            guard !selected else { return }
            Task {
                await store.choose(p, api: model.api)
                if store.current.id == p.id { Haptics.success() }
            }
        } label: {
            VStack(spacing: Theme.Space.s) {
                ZStack {
                    RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                        .fill((p.palette?.primary?.color ?? Theme.accent).opacity(0.12))
                    PersonaAvatar(size: PersonaStore.thumbnailPoints, persona: p)
                        .overlay(alignment: .bottomTrailing) {
                            // Not on the phone yet: choosing it downloads it.
                            if store.downloading.contains(p.id) || uploads.deleting.contains(p.id) {
                                ProgressView().padding(Theme.Space.xxs).background(.regularMaterial, in: .circle)
                            } else if !here {
                                ThemedIcon("arrow.down.circle.fill")
                                    .font(Theme.Font.heading)
                                    .foregroundStyle(.white, Theme.accent)
                                    .accessibilityHidden(true)
                            }
                        }
                }
                .frame(maxWidth: .infinity)
                .frame(height: Self.cardHeight)
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                    .stroke(selected ? Theme.accent : Color.primary.opacity(0.1), lineWidth: selected ? 2.5 : 1))
                VStack(spacing: Theme.Space.xxs) {
                    HStack(spacing: Theme.Space.xs) {
                        if selected { ThemedIcon(Theme.Icon.successFilled).foregroundStyle(Theme.accent) }
                        Text(p.cardTitle).font(Theme.Font.captionStrong).lineLimit(1)
                    }
                    if p.isBuiltin {
                        Text(p.builtinNote).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                    } else if let about = p.manifest?.about, !about.isEmpty {
                        Text(about).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).minimumScaleFactor(0.8)
                    } else if p.isUploaded {
                        // Made from an image: no colors to show; its description, if it has one.
                        Text(p.subtitle.flatMap { $0.isEmpty ? nil : $0 } ?? String(localized: "From your image"))
                            .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).minimumScaleFactor(0.8)
                    } else {
                        PersonaSwatches(palette: p.palette).frame(height: Theme.Space.l)
                    }
                }
            }
            .frame(maxWidth: .infinity)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(p.id == Persona.builtinID ? String(localized: "\(p.cardTitle), default avatar")
                            : p.isBuiltin ? p.cardTitle : here ? p.name : String(localized: "\(p.name), downloads when selected"))
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("persona-\(p.id)")
        .modifier(UploadedAvatarMenu(deletable: uploads.canDelete(p)) { confirmingDelete = p })
        .confirmationDialog(Text("Delete “\(p.name)”?"), isPresented: Binding(get: { confirmingDelete?.id == p.id },
                                                                              set: { if !$0 { confirmingDelete = nil } }),
                            titleVisibility: .visible) {
            Button(String(localized: "Delete Avatar"), role: .destructive) {
                Task { if await uploads.delete(p) { Haptics.success() } else { Haptics.error() } }
            }
            .accessibilityIdentifier("persona-delete-confirm")
        } message: {
            Text("It is deleted from the hub and from your other devices. Where it is in use, the default avatar comes back.")
        }
    }
}

/// An avatar made from an upload: Delete in its card's menu (long press, right click) and as a VoiceOver action.
private struct UploadedAvatarMenu: ViewModifier {
    var deletable: Bool
    var delete: () -> Void

    func body(content: Content) -> some View {
        if deletable {
            content
                .contextMenu {
                    Button(role: .destructive, action: delete) {
                        Label(String(localized: "Delete Avatar"), systemImage: Theme.Icon.delete)
                    }
                }
                .accessibilityAction(named: Text("Delete Avatar"), delete)
        } else {
            content
        }
    }
}
