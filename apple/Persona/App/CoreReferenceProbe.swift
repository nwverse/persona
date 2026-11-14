#if DEBUG || PERF_SIGNPOST
#if os(iOS) || os(visionOS)
import Foundation
import QuartzCore
import UIKit

/// Test-only observations for the iOS reference performance suite (#6871). It never creates a core, injects state or
/// alters navigation: the ordinary native UI runs, and a UI test reads numerical observations through the synthetic hub.
@MainActor
enum CoreReferenceProbe {
    private static var launchedAt: CFTimeInterval?
    private static var firstFrame: Double?
    private static var memorySequence = 0
    private static let processID = UUID().uuidString

    static func launchStarted() {
        guard UserDefaults.standard.bool(forKey: "showCoreReference") else { return }
        launchedAt = CACurrentMediaTime()
        let center = CFNotificationCenterGetDarwinNotifyCenter()
        let callback: CFNotificationCallback = { @Sendable _, _, _, _, _ in
            DispatchQueue.main.async { MainActor.assumeIsolated { CoreReferenceProbe.memorySample() } }
        }
        CFNotificationCenterAddObserver(center, nil, callback, "com.persona.perf.memory" as CFString, nil, .deliverImmediately)
    }

    /// The root view has appeared. This is a view-appearance proxy, not a substitute for XCTApplicationLaunchMetric or
    /// a Core Animation presentation timestamp. The same definition is used for every named-build launch sample.
    static func firstFrameIfNeeded() {
        guard launchedAt != nil, firstFrame == nil else { return }
        firstFrame = (CACurrentMediaTime() - (launchedAt ?? CACurrentMediaTime())) * 1000
        send(["kind": "first_frame", "view_appearance_ms": firstFrame ?? -1])
    }

    static func memorySample() {
        guard UserDefaults.standard.bool(forKey: "showCoreReference") else { return }
        let footprint = FullPageMemory.footprint()
        memorySequence += 1
        send(["kind": "memory", "phys_footprint_bytes": footprint, "sequence": memorySequence])
    }

    private static func send(_ result: [String: Any]) {
        guard let hub = AppModel.shared.api?.config else { return }
        var payload = result
        payload["source"] = "native-ios-process"
        payload["observation_id"] = UUID().uuidString
        payload["process_id"] = processID
        let d = UserDefaults.standard
        if let id = d.string(forKey: "perfRunID") {
            payload["run_id"] = id
            payload["name"] = d.string(forKey: "perfRunName")
            payload["warm_up"] = d.bool(forKey: "perfRunWarmup")
            payload["action"] = d.string(forKey: "perfRunAction")
            payload["expected_iterations"] = d.integer(forKey: "perfRunIterations")
        }
        payload["phys_footprint_bytes"] = FullPageMemory.footprint()
        var info = mach_task_basic_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<mach_task_basic_info_data_t>.size / MemoryLayout<natural_t>.size)
        let result = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(MACH_TASK_BASIC_INFO), $0, &count)
            }
        }
        payload["resident_size_bytes"] = result == KERN_SUCCESS ? Int(info.resident_size) : -1
        payload["is_main_thread"] = Thread.isMainThread
        guard let body = try? JSONSerialization.data(withJSONObject: payload) else { return }
        var request = URLRequest(url: hub.url.appending(path: "mock/frames"), timeoutInterval: 10)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        hub.authorize(&request)
        request.httpBody = body
        Task { _ = try? await URLSession.shared.data(for: request) }
    }
}
#endif
#endif
