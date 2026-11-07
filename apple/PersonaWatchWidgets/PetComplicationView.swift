import SwiftUI
import UIKit
import WidgetKit

/// What a complication draws; the watch app compiles it too, for its debug preview of the faces.
struct PetEntry: TimelineEntry {
    let date: Date
    let snapshot: WatchPetSnapshot?
}

struct PetComplicationView: View {
    let entry: PetEntry
    let family: WidgetFamily

    var body: some View {
        let shown = entry.snapshot?.shown(at: entry.date)
        switch family {
        case .accessoryRectangular:
            HStack {
                face(shown?.state).frame(maxHeight: .infinity)
                VStack(alignment: .leading) {
                    Text(verbatim: shown?.title ?? "Persona").font(.headline).lineLimit(2).minimumScaleFactor(0.8)
                    if shown?.stale == true, let at = entry.snapshot?.at {
                        Text(at, style: .relative).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer(minLength: 0)
            }
        case .accessoryInline:
            Text(verbatim: shown?.title ?? "Persona")
        default:
            face(shown?.state)
        }
    }

    /// The round face for the state; a plain disc before the app has written one (no frog chosen, or not fetched yet).
    @ViewBuilder private func face(_ state: String?) -> some View {
        if let state, let url = WatchPetSnapshot.directory?.appending(path: WatchPetSnapshot.faceName(state)),
           let image = UIImage(contentsOfFile: url.path) {
            Image(uiImage: image).resizable().scaledToFit().clipShape(Circle())
        } else {
            Circle().fill(.gray.opacity(0.3))
        }
    }
}

#if DEBUG
/// `-openWatchItem complication` in the watch app: every family at a 46 mm face's sizes, fresh and stale, on a mock
/// face's grey, since a simulator cannot put a complication on a face from the command line (screenshots).
struct PetComplicationPreview: View {
    var body: some View {
        let fresh = PetEntry(date: .now, snapshot: WatchPetSnapshot.read())
        let stale = PetEntry(date: fresh.snapshot?.staleDate ?? .now, snapshot: fresh.snapshot)
        ScrollView {
            VStack(alignment: .leading, spacing: 8) {
                ForEach([fresh, stale], id: \.date) { entry in
                    HStack(spacing: 8) {
                        PetComplicationView(entry: entry, family: .accessoryCircular).frame(width: 50, height: 50)
                        PetComplicationView(entry: entry, family: .accessoryCorner).frame(width: 32, height: 32)
                        PetComplicationView(entry: entry, family: .accessoryInline).font(.footnote).lineLimit(1)
                    }
                    PetComplicationView(entry: entry, family: .accessoryRectangular).frame(width: 172, height: 76)
                        .background(.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
                }
            }
        }
    }
}
#endif
