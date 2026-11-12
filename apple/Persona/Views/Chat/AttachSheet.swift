#if os(iOS)
import AVFoundation
import BeautifulUI
import Photos
import SwiftUI
import UIKit

// The composer's 「+」 on the iPhone (docs/apple/ios.md, New shell, "Attach panel"): a system sheet that opens at half
// height and drags to full (detents medium and large, the system's grabber), like the system's photo picker. Recent photos
// in three equal square columns (the camera one cell like the others) under a header, and a bar at the sheet's foot
// (Photos, Files, More) that the keyboard pushes up. Photos picked turn the bar into a caption (the draft) and Send.

/// What the composer gets back once the panel has gone (`AttachSheet`).
enum AttachOutcome {
    /// Closed: what was picked goes into the composer, nothing is sent.
    case closed([String])
    /// Send: what was picked goes into the composer and the message is sent.
    case send([String])
    /// A row asked for something the panel cannot show itself (the camera, Files, a voice call): what was picked goes
    /// into the composer, and the action runs once the panel is gone.
    case then([String], () -> Void)
}

/// The panel's tabs, in the bar's order.
enum AttachTab: String, CaseIterable, Identifiable {
    case photos, files, more
    var id: String { rawValue }

    var title: String {
        switch self {
        case .photos: String(localized: "Photos")
        case .files: String(localized: "Files")
        case .more: String(localized: "More")
        }
    }

    var symbol: String {
        switch self {
        case .photos: Theme.Icon.photos
        case .files: Theme.Icon.file
        case .more: Theme.Icon.more
        }
    }
}

/// The panel's state the composer shares: on screen, what is picked, and what to hand back once it has gone.
@Observable
final class AttachPanel {
    /// The sheet is on screen.
    var shown = false
    /// The photos and videos picked, in order. Kept here, not in the sheet, so a swipe down hands them back too.
    var picked: [String] = []
    /// What the composer gets once the sheet is down; nil: closed by the system (a swipe down, a tap outside).
    @ObservationIgnored fileprivate var outcome: AttachOutcome?

    /// A row of the composer's (the More tab) closes the sheet, then acts.
    func close(then action: @escaping () -> Void) { leave(.then(picked, action)) }

    fileprivate func leave(_ outcome: AttachOutcome) {
        guard shown else { return }
        self.outcome = outcome
        shown = false
    }

    /// The panel's photo library and its image manager, kept from one opening to the next. Made on first use: the
    /// chat's `@State` makes an `AttachPanel` on every init of the view, and only the first is kept.
    @ObservationIgnored private(set) lazy var library = AttachLibrary()
}

/// The photo library as the grid needs it: access, the newest photos and videos, thumbnails and full pictures.
@Observable
final class AttachLibrary {
    enum Access { case unknown, full, limited, denied }
    private(set) var access: Access = .unknown
    private(set) var assets: [PHAsset] = []
    @ObservationIgnored private let manager = PHCachingImageManager()
    private static var warmed = false

    /// Opens the Photos library once, off the main thread, so the first 「+」 doesn't wait for it: the first query in a
    /// process loads the Photos database, which took 50 ms to over a second on the main thread (#4250). Only when access
    /// was already given: this never asks.
    static func prewarm() {
        guard !warmed else { return }
        warmed = true
        Task.detached(priority: .utility) {
            let status = PHPhotoLibrary.authorizationStatus(for: .readWrite)
            guard status == .authorized || status == .limited else { return }
            _ = newest(limit: 1)
        }
    }

    func load() async {
        var status = PHPhotoLibrary.authorizationStatus(for: .readWrite)
        if status == .notDetermined { status = await PHPhotoLibrary.requestAuthorization(for: .readWrite) }
        switch status {
        case .authorized: access = .full
        case .limited: access = .limited
        default: access = .denied; assets = []; return
        }
        await fetch()
    }

    /// Lets go of the photos when the panel goes: the next opening draws its light loading state first and the grid a
    /// moment later, rather than the whole grid in the frame the panel presents in (#4250).
    func forget() { access = .unknown; assets = [] }

    /// The newest photos and videos, fetched off the main thread (#4250).
    func fetch() async {
        let limit = Theme.Attach.fetchLimit
        let fetched = await Task.detached(priority: .userInitiated) { Self.newest(limit: limit) }.value
        // The panel went while it was fetching (`forget()` has run): keep nothing for the next opening.
        guard !Task.isCancelled else { return }
        assets = fetched.assets
    }

    private struct Fetched: @unchecked Sendable { let assets: [PHAsset] }

    private nonisolated static func newest(limit: Int) -> Fetched {
        let options = PHFetchOptions()
        options.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
        options.predicate = NSPredicate(format: "mediaType == %d || mediaType == %d", PHAssetMediaType.image.rawValue, PHAssetMediaType.video.rawValue)
        options.fetchLimit = limit
        let result = PHAsset.fetchAssets(with: options)
        var list: [PHAsset] = []
        list.reserveCapacity(result.count)
        result.enumerateObjects { asset, _, _ in list.append(asset) }
        return Fetched(assets: list)
    }

    func thumbnail(_ asset: PHAsset, side: CGFloat) async -> UIImage? {
        let options = PHImageRequestOptions()
        options.deliveryMode = .highQualityFormat
        options.resizeMode = .fast
        options.isNetworkAccessAllowed = true
        let size = CGSize(width: side * Theme.Attach.thumbnailScale, height: side * Theme.Attach.thumbnailScale)
        return await withCheckedContinuation { c in
            manager.requestImage(for: asset, targetSize: size, contentMode: .aspectFill, options: options) { @Sendable image, _ in
                c.resume(returning: image)
            }
        }
    }

    /// The whole picture, downloaded from iCloud if it has to be.
    static func picture(_ asset: PHAsset) async -> UIImage? {
        let options = PHImageRequestOptions()
        options.isNetworkAccessAllowed = true
        options.deliveryMode = .highQualityFormat
        return await withCheckedContinuation { c in
            PHImageManager.default().requestImageDataAndOrientation(for: asset, options: options) { @Sendable data, _, _, _ in
                c.resume(returning: data.flatMap(UIImage.init(data:)))
            }
        }
    }

    /// A copy of the video in a directory of its own under the app's temporary uploads, named as in Photos, for the
    /// upload (`VideoAttachments.start(staged:)`, which deletes it). A slow-motion or edited video is a composition with
    /// no file: its original goes instead.
    static func movie(_ asset: PHAsset) async -> URL? {
        var found = await movieFile(asset, version: .current)
        if found == nil { found = await movieFile(asset, version: .original) }
        guard let source = found else { return nil }
        let name = PHAssetResource.assetResources(for: asset).first?.originalFilename ?? source.lastPathComponent
        return await Task.detached(priority: .userInitiated) { () -> URL? in
            let dir = FileManager.default.temporaryDirectory.appendingPathComponent("uploads").appendingPathComponent(UUID().uuidString)
            let copy = dir.appendingPathComponent(name)
            do {
                try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                try FileManager.default.copyItem(at: source, to: copy)
                return copy
            } catch {
                try? FileManager.default.removeItem(at: dir)
                return nil
            }
        }.value
    }

    private static func movieFile(_ asset: PHAsset, version: PHVideoRequestOptionsVersion) async -> URL? {
        let options = PHVideoRequestOptions()
        options.isNetworkAccessAllowed = true
        options.version = version
        return await withCheckedContinuation { c in
            PHImageManager.default().requestAVAsset(forVideo: asset, options: options) { @Sendable av, _, _ in
                c.resume(returning: (av as? AVURLAsset)?.url)
            }
        }
    }

    static func asset(_ id: String) -> PHAsset? {
        PHAsset.fetchAssets(withLocalIdentifiers: [id], options: nil).firstObject
    }
}

/// The panel: the content of the composer's sheet (`AttachSheetHost`), which sets its detents.
struct AttachSheet<More: View>: View {
    let panel: AttachPanel
    @Binding var caption: String
    /// How many more photos the composer takes (it holds four), and videos.
    var photoRoom: Int
    var videoRoom: Int
    /// Takes a photo with the camera (nil: no camera); opens Files; opens the system's photo picker.
    var camera: (() -> Void)?
    var files: () -> Void
    var allPhotos: () -> Void
    var showsMore: Bool
    @ViewBuilder var more: () -> More

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.buiTheme) private var theme
    private var library: AttachLibrary { panel.library }
    @State private var tab: AttachTab = .photos
    @State private var detent: PresentationDetent = .medium
    @FocusState private var captionFocused: Bool

    private var tabs: [AttachTab] { showsMore ? AttachTab.allCases : [.photos, .files] }
    private var surface: Color { AppStyle.current.usesBeautifulUI ? theme.colors.surface : Theme.Shell.composer }
    private var picked: [String] { panel.picked }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AttachSheet")
        #endif
        GeometryReader { outer in
            let width = outer.size.width
            VStack(spacing: Theme.Space.flush) {
                header
                page(width: width)
            }
            // The bar sits on the sheet's safe area: over the home indicator, or over the keyboard while the caption is
            // typed, so the field and what is typed stay in sight (#3704); the grid scrolls under it to its last row.
            .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) { bar(width: width) }
        }
        .presentationDetents([.medium, .large], selection: $detent)
        .presentationDragIndicator(.visible)
        .presentationBackground(surface)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("attach-sheet")
        .task { await library.load() }
        .onDisappear { library.forget() }
    }

    private var header: some View {
        ZStack {
            Text(tab == .photos ? String(localized: "Recents") : tab.title)
                .font(Theme.Font.heading)
                .lineLimit(1)
                .contentTransition(.opacity)
                .accessibilityAddTraits(.isHeader)
            HStack {
                Button { leave(.closed(picked)) } label: {
                    ThemedIcon(Theme.Icon.close).font(Theme.Font.heading)
                        .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Close")
                .accessibilityIdentifier("attach-close")
                Spacer()
                if tab == .photos {
                    Button { leave(.then(picked, allPhotos)) } label: {
                        Text("All Photos").font(Theme.Font.secondaryMedium).lineLimit(1)
                            .scaledFrame(minHeight: Theme.Size.tapTarget)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(Theme.accent)
                    .accessibilityIdentifier("attach-all-photos")
                }
            }
            .padding(.horizontal, Theme.Space.m)
        }
        // Under the system's grabber.
        .padding(.top, Theme.Space.l)
        .padding(.bottom, Theme.Space.xs)
        .frame(maxWidth: .infinity)
    }

    // MARK: Pages

    @ViewBuilder private func page(width: CGFloat) -> some View {
        ZStack(alignment: .top) {
            switch tab {
            case .photos: photos(width: width).transition(.asymmetric(insertion: .identity, removal: .opacity))
            case .files: filesPage.transition(.asymmetric(insertion: .identity, removal: .opacity))
            case .more: morePage.transition(.asymmetric(insertion: .identity, removal: .opacity))
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
    }

    @ViewBuilder private func photos(width: CGFloat) -> some View {
        switch library.access {
        case .unknown:
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        case .denied:
            AttachNoAccess(settings: openSettings, allPhotos: { leave(.then(picked, allPhotos)) })
        case .full, .limited:
            AttachGrid(library: library, picked: Bindable(panel).picked, width: width,
                       camera: camera.map { camera in { leave(.then(picked, camera)) } },
                       limited: library.access == .limited, photoRoom: photoRoom, videoRoom: videoRoom)
        }
    }

    private var filesPage: some View {
        List {
            Section {
                Button { leave(.then(picked, files)) } label: {
                    Label { Text("Browse Files").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.file) }
                }
                .accessibilityIdentifier("attach-browse-files")
            } footer: {
                Text("A file goes up to this conversation's computer, and its path into your message.")
            }
        }
        .scrollContentBackground(.hidden)
    }

    private var morePage: some View {
        List { more() }
            .scrollContentBackground(.hidden)
    }

    // MARK: The bar

    /// The tabs, or once photos are picked the caption and Send.
    @ViewBuilder private func bar(width: CGFloat) -> some View {
        ZStack(alignment: .bottom) {
            if picked.isEmpty {
                AttachTabBar(tabs: tabs, selected: tab, width: AttachLayout.barWidth(tabs: tabs.count, width: width), pick: pick)
                    .transition(.opacity)
            } else {
                captionBar
                    .transition(.opacity)
            }
        }
        .padding(.bottom, Theme.Space.s)
        .frame(maxWidth: .infinity)
        .animation(reduceMotion ? nil : Theme.Motion.attachBarSwap, value: picked.isEmpty)
    }

    private var captionBar: some View {
        HStack(alignment: .bottom, spacing: Theme.Space.xs) {
            TextField("Add a caption…", text: $caption, axis: .vertical)
                .lineLimit(1...4)
                .font(Theme.Font.body)
                .focused($captionFocused)
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.s)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
                .modifier(ComposerSurface())
                .accessibilityIdentifier("attach-caption")
            HStack(spacing: Theme.Space.flush) {
                ComposerRoundButton(symbol: Theme.Icon.send, label: String(localized: "Send"), id: "attach-send", pressScales: true) {
                    leave(.send(picked))
                }
            }
            .transition(.composerSend)
        }
        .padding(.horizontal, Theme.Space.l)
    }

    // MARK: Actions

    /// The sheet goes down (the system's motion), then the composer gets `outcome` (`AttachSheetHost`).
    private func leave(_ outcome: AttachOutcome) {
        captionFocused = false
        panel.leave(outcome)
    }

    private func pick(_ next: AttachTab) {
        if next == tab {
            detent = .large // the tab already shown: open all the way
            return
        }
        withAnimation(reduceMotion ? nil : Theme.Motion.attachTabFade) { tab = next }
    }

    private func openSettings() {
        if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
    }
}

/// The floating tab bar: a pill of tabs, a highlight sliding under the one shown.
struct AttachTabBar: View {
    var tabs: [AttachTab]
    var selected: AttachTab
    var width: CGFloat
    var pick: (AttachTab) -> Void
    @Namespace private var lens

    var body: some View {
        HStack(spacing: Theme.Space.flush) {
            ForEach(tabs) { tab in
                Button { pick(tab) } label: {
                    VStack(spacing: Theme.Space.xxs) {
                        ThemedIcon(tab.symbol)
                            .font(Theme.Font.heading)
                            .frame(width: Theme.Attach.barIcon, height: Theme.Attach.barIcon)
                        Text(tab.title).font(Theme.Font.labelMedium).lineLimit(1)
                    }
                    .foregroundStyle(tab == selected ? Theme.accent : Color.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background {
                        if tab == selected {
                            Capsule().fill(Theme.Fill.chip).matchedGeometryEffect(id: "lens", in: lens)
                        }
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(tab.title)
                .accessibilityAddTraits(tab == selected ? .isSelected : [])
                .accessibilityIdentifier("attach-tab-\(tab.rawValue)")
            }
        }
        .padding(Theme.Attach.barLensInset)
        .frame(width: width, height: Theme.Attach.barHeight)
        .modifier(ComposerSurface())
        .animation(Theme.Motion.attachLens, value: selected)
    }
}

/// The grid of recent photos: three equal square columns, the camera first as one cell like the others.
private struct AttachGrid: View {
    let library: AttachLibrary
    @Binding var picked: [String]
    var width: CGFloat
    var camera: (() -> Void)?
    var limited: Bool
    var photoRoom: Int
    var videoRoom: Int
    @Environment(\.displayScale) private var displayScale

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AttachGrid")
        #endif
        let cell = AttachLayout.cell(width: width, scale: displayScale)
        let spacing = Theme.Attach.gridSpacing
        let columns = Array(repeating: GridItem(.fixed(cell), spacing: spacing), count: Theme.Attach.columns)
        ScrollView {
            VStack(spacing: Theme.Space.flush) {
                if limited { AttachLimitedBanner(refetch: { await library.fetch() }) }
                LazyVGrid(columns: columns, spacing: spacing) {
                    if let camera {
                        AttachCameraCell(action: camera).frame(width: cell, height: cell)
                    }
                    ForEach(library.assets, id: \.localIdentifier) { photo($0, cell: cell) }
                }
            }
        }
        .scrollIndicators(.hidden)
        .accessibilityIdentifier("attach-grid")
    }

    private func photo(_ asset: PHAsset, cell: CGFloat) -> some View {
        let id = asset.localIdentifier
        return AttachPhotoCell(library: library, asset: asset, side: cell, number: picked.firstIndex(of: id).map { $0 + 1 }) {
            toggle(asset)
        }
    }

    private func toggle(_ asset: PHAsset) {
        let id = asset.localIdentifier
        if let i = picked.firstIndex(of: id) { picked.remove(at: i); return }
        let video = asset.mediaType == .video
        let same = picked.compactMap(AttachLibrary.asset).filter { ($0.mediaType == .video) == video }.count
        guard same < (video ? videoRoom : photoRoom) else {
            AppModel.shared.toast = Toast(String(localized: "Up to 4 at a time"), style: .error)
            return
        }
        picked.append(id)
        Haptics.tap()
    }
}

/// One photo or video in the grid: its thumbnail, a video's length, and the mark that says it is picked and in what order.
private struct AttachPhotoCell: View {
    let library: AttachLibrary
    let asset: PHAsset
    var side: CGFloat
    var number: Int?
    var toggle: () -> Void
    @State private var image: UIImage?

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AttachPhotoCell")
        #endif
        Button(action: toggle) {
            ZStack(alignment: .topTrailing) {
                Group {
                    if let image { Image(uiImage: image).resizable().scaledToFill() } else { Theme.Fill.well }
                }
                .frame(width: side, height: side)
                .clipped()
                if asset.mediaType == .video {
                    Text(Duration.seconds(asset.duration).formatted(.time(pattern: .minuteSecond)))
                        .font(Theme.Font.labelStrong)
                        .foregroundStyle(.white)
                        .shadow(radius: Theme.Space.xxs)
                        .padding(Theme.Space.xs)
                        .frame(width: side, height: side, alignment: .bottomLeading)
                }
                AttachPickMark(number: number)
                    .padding(Theme.Attach.badgeInset)
            }
            // Taps where it is drawn: the thumbnail filling the square is laid out past it (a landscape photo, wider than
            // tall), and the cell to the right, drawn over it, took the taps on this one's ring (#3702).
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .frame(width: side, height: side)
        .task(id: asset.localIdentifier) { image = await library.thumbnail(asset, side: side) }
        .accessibilityLabel(asset.mediaType == .video ? String(localized: "Video") : String(localized: "Photo"))
        .accessibilityValue(number.map { String(localized: "Picked, \($0)") } ?? "")
        .accessibilityAddTraits(number != nil ? .isSelected : [])
        .accessibilityIdentifier("attach-photo")
    }
}

/// The round mark in a cell's corner: an empty ring, or filled with the photo's place in the order, bouncing as it fills.
private struct AttachPickMark: View {
    var number: Int?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ZStack {
            Circle().fill(number == nil ? Color.black.opacity(Theme.Attach.dim) : Theme.accent)
            Circle().strokeBorder(.white, lineWidth: Theme.Attach.badgeRing)
            if let number {
                Text(verbatim: "\(number)").font(Theme.Font.labelStrong).foregroundStyle(.white).monospacedDigit()
            }
        }
        .frame(width: Theme.Attach.badge, height: Theme.Attach.badge)
        .keyframeAnimator(initialValue: CGFloat(1), trigger: number) { mark, scale in
            mark.scaleEffect(scale)
        } keyframes: { _ in
            if number != nil, !reduceMotion {
                CubicKeyframe(Theme.Motion.attachPickSqueeze, duration: Theme.Motion.attachPickSqueezeTime)
                CubicKeyframe(Theme.Motion.attachPickBounce, duration: Theme.Motion.attachPickBounceTime)
                CubicKeyframe(1, duration: Theme.Motion.attachPickSettleTime)
            } else {
                CubicKeyframe(1, duration: Theme.Motion.attachPickSettleTime)
            }
        }
        .animation(number == nil ? Theme.Motion.attachUnpick : Theme.Motion.attachPick, value: number)
        .shadow(color: .black.opacity(Theme.Attach.dim), radius: Theme.Space.xxs)
    }
}

/// The camera's cell: takes a photo with the system camera (the panel goes first). Telegram shows the live camera here.
private struct AttachCameraCell: View {
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            ZStack {
                Theme.Fill.chip
                ThemedIcon(Theme.Icon.camera).font(Theme.Font.title).foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Take Photo")
        .accessibilityIdentifier("attach-camera")
    }
}

/// Limited access: a line over the grid saying so, and Manage to pick more.
private struct AttachLimitedBanner: View {
    var refetch: () async -> Void

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            Text("Persona can see only the photos you chose.")
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
            Button("Manage") {
                guard let top = AttachTop.controller() else { return }
                PHPhotoLibrary.shared().presentLimitedLibraryPicker(from: top) { _ in Task { @MainActor in await refetch() } }
            }
            .font(Theme.Font.secondaryStrong)
            .lineLimit(1)
            .accessibilityIdentifier("attach-manage")
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
    }
}

/// No access to the photos: say so, offer Settings, and the system's picker (which needs none).
private struct AttachNoAccess: View {
    var settings: () -> Void
    var allPhotos: () -> Void

    var body: some View {
        VStack(spacing: Theme.Space.m) {
            ThemedIcon(Theme.Icon.photos).font(Theme.Font.display).foregroundStyle(.secondary)
            Text("Access Your Photos").font(Theme.Font.heading)
            Text("Let Persona see your photos to pick them here, or choose them in the system's picker.")
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Button("Open Settings", action: settings)
                .themedButton(prominent: true)
                .lineLimit(1)
            Button("Choose Photos", action: allPhotos)
                .font(Theme.Font.bodyStrong)
                .lineLimit(1)
                .accessibilityIdentifier("attach-choose-photos")
        }
        .padding(.horizontal, Theme.Space.xxl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Puts the panel in a system sheet from the composer, and hands back what it gave once the sheet is down.
struct AttachSheetHost<More: View>: ViewModifier {
    let panel: AttachPanel
    @Binding var caption: String
    var photoRoom: Int
    var videoRoom: Int
    var camera: (() -> Void)?
    var files: () -> Void
    var allPhotos: () -> Void
    var showsMore: Bool
    @ViewBuilder var more: () -> More
    var finish: (AttachOutcome) -> Void

    func body(content: Content) -> some View {
        content.sheet(isPresented: Binding(get: { panel.shown }, set: { panel.shown = $0 }), onDismiss: {
            // Closed by the system (a swipe down, a tap outside): what was picked goes into the composer.
            let outcome = panel.outcome ?? .closed(panel.picked)
            panel.outcome = nil
            panel.picked = []
            // What runs after it (the camera, Files) presents now that the sheet is down.
            finish(outcome)
        }) {
            AttachSheet(panel: panel, caption: $caption, photoRoom: photoRoom, videoRoom: videoRoom, camera: camera, files: files,
                        allPhotos: allPhotos, showsMore: showsMore, more: more)
        }
    }
}

/// The view controller on top, for UIKit sheets the panel presents itself (the limited-library picker).
private enum AttachTop {
    static func controller() -> UIViewController? {
        let window = UIApplication.shared.connectedScenes.compactMap { ($0 as? UIWindowScene)?.keyWindow }.first
        var top = window?.rootViewController
        while let next = top?.presentedViewController, !next.isBeingDismissed { top = next }
        return top
    }
}

/// A row of the panel's More tab (the menu's other items); it closes the panel, then acts.
struct AttachMoreRow: Identifiable {
    let id: String
    let title: String
    let symbol: String
    var destructive = false
    var disabled = false
    let action: () -> Void
}

struct AttachMoreSection: Identifiable {
    let id: String
    let title: String?
    let rows: [AttachMoreRow]
}

struct AttachMoreRows: View {
    let panel: AttachPanel
    let sections: [AttachMoreSection]

    var body: some View {
        ForEach(sections) { section in
            Section {
                ForEach(section.rows) { row in
                    Button(role: row.destructive ? .destructive : nil) { panel.close(then: row.action) } label: {
                        Label { Text(row.title).lineLimit(1) } icon: { ThemedIcon(row.symbol) }
                    }
                    .disabled(row.disabled)
                    .accessibilityIdentifier(row.id)
                }
            } header: {
                if let title = section.title { Text(title) }
            }
        }
    }
}

extension AttachPanel {
    /// Opens the sheet, nothing picked yet.
    func open() {
        picked = []
        outcome = nil
        shown = true
    }
}
#endif
