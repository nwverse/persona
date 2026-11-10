#if (DEBUG || PERF_SIGNPOST) && (os(iOS) || os(visionOS))
import SwiftUI
import UIKit

/// `probe.agents-scroll` (docs/apple/ios-state.md「Probes and assertions」): where the top of the Agents list is, read from UIKit every frame,
/// for the tests of going into a chat and back (AgentsReturnTests) and of the shell's status row. The navigation bar's
/// y, the search field's height, the list's top inset and the first row's y (window points), now; where they were when
/// the list was left for a pushed page; and over every frame of the last way back, from the first frame of the pop to a
/// while after it, the least and most of each, and the largest jump of the row in one frame. Coming back from a chat
/// once moved the list 36 pt down, opened the search field and sprang up 52 pt after the pop (#340).
struct AgentsScrollProbe: View {
    var page: String?
    @State private var sampler = AgentsScrollSampler()

    var body: some View {
        SamplerHost(sampler: sampler)
            .screenProbe("agents-scroll", page: page, sampler.value)
    }
}

/// Where the list's top is in one frame.
struct AgentsScrollPlace: Encodable, Equatable {
    var nav: CGFloat
    var search: CGFloat
    var inset: CGFloat
    var row: CGFloat
    /// The navigation container's y and top safe area: they stay put (the shell's room is the page's own, #340).
    var container: CGFloat
    var safe: CGFloat
}

/// Every frame of one way back to the list.
struct AgentsScrollReturn: Encodable, Equatable {
    /// Ways back finished so far.
    var n = 0
    var frames = 0
    var row = Span(), nav = Span(), search = Span(), inset = Span(), container = Span(), safe = Span()
    /// The largest change of the row's y from one frame to the next.
    var rowStep: CGFloat = 0

    struct Span: Encodable, Equatable {
        var min: CGFloat = .infinity
        var max: CGFloat = -.infinity
        mutating func add(_ v: CGFloat) { min = Swift.min(min, v); max = Swift.max(max, v) }
    }
}

struct AgentsScrollValue: Encodable, Equatable {
    var now: AgentsScrollPlace?
    /// Where it was the frame before a page was pushed over the list.
    var left: AgentsScrollPlace?
    var back: AgentsScrollReturn?
}

@Observable @MainActor final class AgentsScrollSampler {
    var value = AgentsScrollValue()
}

private struct SamplerHost: UIViewControllerRepresentable {
    let sampler: AgentsScrollSampler

    func makeUIViewController(context: Context) -> Sampling { Sampling(sampler: sampler) }
    func updateUIViewController(_ c: Sampling, context: Context) {}

    /// A child of the list page's controller: told when the page goes and comes back.
    final class Sampling: UIViewController {
        private let sampler: AgentsScrollSampler
        private var link: CADisplayLink?
        private var last: AgentsScrollPlace?
        /// The row followed: the first one on screen when the list was left.
        private var followed: IndexPath?
        private var trip: AgentsScrollReturn?
        private var settleUntil: CFTimeInterval?
        private var lastRow: CGFloat?
        private var returns = 0
        /// How long after the pop the way back is still watched: the list sprang 0.4–0.6 s after it.
        private static let settle: CFTimeInterval = 1

        init(sampler: AgentsScrollSampler) {
            self.sampler = sampler
            super.init(nibName: nil, bundle: nil)
        }

        required init?(coder: NSCoder) { fatalError() }

        override func loadView() {
            view = UIView()
            view.isUserInteractionEnabled = false
        }

        override func viewDidLoad() {
            super.viewDidLoad()
            let l = CADisplayLink(target: Tick(self), selector: #selector(Tick.tick))
            l.add(to: .main, forMode: .common)
            link = l
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            guard let last else { return }
            followed = nil
            followed = topRow()?.path
            sampler.value.left = last
            trip = nil
            settleUntil = nil
        }

        override func viewWillAppear(_ animated: Bool) {
            super.viewWillAppear(animated)
            guard sampler.value.left != nil else { return } // the first time: nothing to come back from
            trip = AgentsScrollReturn()
            settleUntil = nil
            lastRow = nil
            transitionCoordinator?.notifyWhenInteractionChanges { [weak self] c in
                if c.isCancelled { self?.trip = nil }
            }
        }

        override func viewDidAppear(_ animated: Bool) {
            super.viewDidAppear(animated)
            if trip != nil { settleUntil = CACurrentMediaTime() + Self.settle }
        }

        fileprivate func tick() {
            guard let place = sample() else { return }
            last = place
            if var t = trip {
                t.frames += 1
                t.row.add(place.row); t.nav.add(place.nav); t.search.add(place.search)
                t.inset.add(place.inset); t.container.add(place.container); t.safe.add(place.safe)
                if let lastRow { t.rowStep = max(t.rowStep, abs(place.row - lastRow)) }
                lastRow = place.row
                if let end = settleUntil, CACurrentMediaTime() >= end, t.frames > 0 {
                    returns += 1
                    t.n = returns
                    sampler.value.back = t
                    trip = nil
                    settleUntil = nil
                    followed = nil
                } else {
                    trip = t
                }
            }
            if sampler.value.now != place { sampler.value.now = place }
        }

        private func sample() -> AgentsScrollPlace? {
            guard let window = view.window, let page = parent, let nav = page.navigationController,
                  let list = Self.largestList(in: page.view), let row = topRow(in: list) else { return nil }
            func y(_ v: UIView) -> CGFloat { v.convert(v.bounds, to: window).minY }
            let bar = nav.navigationBar
            let search = page.navigationItem.searchController?.searchBar
            return AgentsScrollPlace(nav: Self.round(y(bar)),
                                     search: Self.round(search?.window == nil ? 0 : search?.bounds.height ?? 0),
                                     inset: Self.round(list.adjustedContentInset.top),
                                     row: Self.round(row.y), container: Self.round(y(nav.view)),
                                     safe: Self.round(nav.view.safeAreaInsets.top))
        }

        private func topRow(in list: UICollectionView? = nil) -> (path: IndexPath, y: CGFloat)? {
            guard let list = list ?? parent.flatMap({ Self.largestList(in: $0.view) }), let window = view.window else { return nil }
            if let followed, let cell = list.cellForItem(at: followed) {
                return (followed, cell.convert(cell.bounds, to: window).minY)
            }
            let top = list.visibleCells.min { $0.frame.minY < $1.frame.minY }
            guard let top, let path = list.indexPath(for: top) else { return nil }
            return (path, top.convert(top.bounds, to: window).minY)
        }

        private static func largestList(in root: UIView?) -> UICollectionView? {
            guard let root else { return nil }
            var best: UICollectionView?
            var stack = [root]
            while let v = stack.popLast() {
                if let c = v as? UICollectionView, !c.isHidden, c.bounds.height > (best?.bounds.height ?? 0) { best = c }
                stack.append(contentsOf: v.subviews)
            }
            return best
        }

        /// Half points: what a frame can show.
        private static func round(_ v: CGFloat) -> CGFloat { (v * 2).rounded() / 2 }
    }

    /// The display link holds its target: this, not the controller.
    final class Tick: NSObject {
        weak var owner: Sampling?
        init(_ owner: Sampling) { self.owner = owner }
        @objc func tick() { owner?.tick() }
    }
}
#endif
