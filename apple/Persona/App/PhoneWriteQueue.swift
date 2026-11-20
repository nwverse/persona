import Foundation
import Observation

extension APIClient {
    /// Older hubs return the retained records as well. Decode only their shared queued + delivering count.
    func pendingPhoneWriteCount() async throws -> Int? {
        nonisolated struct Summary: Decodable, Sendable {
            let pendingCount: Int?
            enum CodingKeys: String, CodingKey { case pendingCount = "pending_count" }
        }
        let summary: Summary = try await manager(nil).getDecodedOffMain("/api/v1/phone/write-queue", query: ["count_only": "true"])
        return summary.pendingCount.flatMap { $0 >= 0 ? $0 : nil }
    }
}

extension HubLink {
    /// The authenticated connection that can answer for this particular pairing. A reconnect is a fresh read.
    var phoneWriteSource: PhoneWriteQueue.Source? {
        guard !isDemo, live.state == .connected, !live.stalled else { return nil }
        return .init(link: ObjectIdentifier(self), config: config, generation: live.generation)
    }
}

/// The summary is shared by the settings page and the single-write prompt while either is on screen.
@MainActor @Observable final class PhoneWriteQueue {
    nonisolated struct Source: Hashable, Sendable {
        let link: ObjectIdentifier
        let config: ServerConfig
        let generation: Int
    }

    private var source: Source?
    private var count: Int?
    @ObservationIgnored private var viewers: [UUID: AsyncStream<Void>.Continuation] = [:]
    @ObservationIgnored private var polling: Task<Void, Never>?
    @ObservationIgnored private var client: APIClient?

    func count(for visible: Source?) -> Int? {
        guard let visible, source == visible, let count, count > 0 else { return nil }
        return count
    }

    /// One poll across both surfaces. Cancelling the last visible view also clears the stored count.
    func follow(_ visible: Source, using api: APIClient) async {
        guard !Task.isCancelled else { return }
        if source != visible {
            reset()
            source = visible
            client = api
            poll(visible, using: api)
        }
        let id = UUID()
        let (lifetime, end) = AsyncStream<Void>.makeStream()
        viewers[id] = end
        defer {
            if viewers.removeValue(forKey: id) != nil && viewers.isEmpty { reset() }
        }
        for await _ in lifetime {}
    }

    /// A local confirmation now has a WebSocket result. Give the hub time to record it and read its authoritative count.
    func resultSent() {
        guard let source, let client else { return }
        poll(source, using: client, delay: .milliseconds(300))
    }

    private func reset() {
        polling?.cancel()
        polling = nil
        client = nil
        source = nil
        count = nil
        for end in viewers.values { end.finish() }
        viewers.removeAll()
    }

    private func poll(_ visible: Source, using api: APIClient, delay: Duration = .zero) {
        polling?.cancel()
        polling = Task { [weak self] in
            if delay != .zero { try? await Task.sleep(for: delay) }
            while !Task.isCancelled {
                let next: Int?
                let retry: Bool
                do {
                    next = try await api.pendingPhoneWriteCount()
                    retry = next != nil
                } catch let error as APIError where [401, 403, 404].contains(error.status) {
                    next = nil
                    retry = false
                } catch is DecodingError {
                    next = nil
                    retry = false
                } catch {
                    next = nil
                    retry = true
                }
                guard !Task.isCancelled, self?.source == visible else { return }
                self?.count = next
                guard retry else { self?.client = nil; return }
                // A different phone can finish a write without a message on this socket.
                try? await Task.sleep(for: .seconds(30))
            }
        }
    }
}
