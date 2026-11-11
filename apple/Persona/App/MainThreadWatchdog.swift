#if DEBUG || PERF_SIGNPOST
import Darwin
import Foundation
import QuartzCore
import Synchronization

nonisolated struct MainThreadBlock: Sendable {
    let durationMS: Double
    let backtrace: [String]
}

nonisolated struct MainThreadWatchdogSnapshot: Sendable {
    let count: Int
    let maxMS: Double
    let longest: [MainThreadBlock]
}

// The signal handler runs on the main thread and only copies return addresses. Symbolication happens afterwards on the
// watchdog thread. Atomic state publishes the addresses without taking a lock in the handler.
private nonisolated(unsafe) let perfMainThreadCaptureState = Atomic<Int32>(0)
private nonisolated(unsafe) var perfMainThreadFrames = [UnsafeMutableRawPointer?](repeating: nil, count: 64)
private nonisolated(unsafe) var perfMainThreadFrameCount: Int32 = 0

private nonisolated func perfMainThreadSignalHandler(_: Int32) {
    guard perfMainThreadCaptureState.load(ordering: .acquiring) == 1 else { return }
    perfMainThreadFrames.withUnsafeMutableBufferPointer { frames in
        perfMainThreadFrameCount = backtrace(frames.baseAddress, Int32(frames.count))
    }
    perfMainThreadCaptureState.store(2, ordering: .releasing)
}

/// Reports main-thread blocks in DEBUG and PERF_SIGNPOST builds. A background thread pings the main run loop; after the
/// threshold it asks the main thread to sample itself with a directed signal, then waits for the ping to learn the full
/// block duration. No stack is captured or signpost emitted while the main thread answers on time.
nonisolated final class MainThreadWatchdog: @unchecked Sendable {
    static let shared = MainThreadWatchdog()
    private static let retainedBlocks = 5

    private let lock = NSLock()
    private var worker: Thread?
    private var mainThread: pthread_t?
    private var generation = 0
    private var count = 0
    private var maxMS = 0.0
    private var longest: [MainThreadBlock] = []

    private init() {}

    func start(threshold: TimeInterval = 0.1, interval: TimeInterval = 0.025) {
        lock.lock()
        guard worker == nil else { lock.unlock(); return }
        generation += 1
        let generation = self.generation
        mainThread = pthread_self()
        perfMainThreadCaptureState.store(0, ordering: .releasing)
        _ = signal(SIGUSR1, perfMainThreadSignalHandler)
        // Warm up the unwinder outside the signal handler; its first call may need to initialize metadata.
        var warm = [UnsafeMutableRawPointer?](repeating: nil, count: 2)
        _ = backtrace(&warm, Int32(warm.count))
        let thread = Thread { @Sendable [self] in run(threshold: threshold, interval: interval, generation: generation) }
        worker = thread
        lock.unlock()
        thread.start()
    }

    func stop() {
        lock.lock()
        generation += 1
        worker?.cancel()
        worker = nil
        mainThread = nil
        perfMainThreadCaptureState.store(0, ordering: .releasing)
        lock.unlock()
    }

    func reset() {
        lock.lock()
        count = 0
        maxMS = 0
        longest = []
        lock.unlock()
    }

    var snapshot: MainThreadWatchdogSnapshot {
        lock.lock()
        defer { lock.unlock() }
        return MainThreadWatchdogSnapshot(count: count, maxMS: maxMS, longest: longest)
    }

    /// JSON-ready counters and the longest stacks for `PerfCount` and the performance UI tests' `frames.json`.
    var report: [String: Any] {
        let snapshot = snapshot
        return [
            "count": snapshot.count,
            "max_ms": snapshot.maxMS,
            "longest": snapshot.longest.map { ["duration_ms": $0.durationMS, "backtrace": $0.backtrace] },
        ]
    }

    private func running(_ generation: Int) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return worker != nil && self.generation == generation
    }

    private func run(threshold: TimeInterval, interval: TimeInterval, generation: Int) {
        let answered = DispatchSemaphore(value: 0)
        while running(generation), !Thread.current.isCancelled {
            let started = CACurrentMediaTime()
            DispatchQueue.main.async { answered.signal() }
            if answered.wait(timeout: .now() + threshold) == .success {
                Thread.sleep(forTimeInterval: interval)
                continue
            }

            let stack = sampleMainThread()
            while answered.wait(timeout: .now() + 0.01) == .timedOut {
                guard running(generation), !Thread.current.isCancelled else { return }
            }
            let durationMS = (CACurrentMediaTime() - started) * 1000
            record(MainThreadBlock(durationMS: durationMS, backtrace: stack), generation: generation)
            Thread.sleep(forTimeInterval: interval)
        }
    }

    private func sampleMainThread() -> [String] {
        lock.lock()
        let main = mainThread
        lock.unlock()
        guard let main else { return ["main thread unavailable"] }
        perfMainThreadCaptureState.store(1, ordering: .releasing)
        guard pthread_kill(main, SIGUSR1) == 0 else {
            perfMainThreadCaptureState.store(0, ordering: .releasing)
            return ["could not signal the main thread"]
        }
        let deadline = CACurrentMediaTime() + 0.05
        while perfMainThreadCaptureState.load(ordering: .acquiring) != 2, CACurrentMediaTime() < deadline {
            Thread.sleep(forTimeInterval: 0.001)
        }
        guard perfMainThreadCaptureState.load(ordering: .acquiring) == 2 else {
            perfMainThreadCaptureState.store(0, ordering: .releasing)
            return ["main thread did not enter the sampling signal handler"]
        }
        let count = Int(perfMainThreadFrameCount)
        let symbols = perfMainThreadFrames.withUnsafeBufferPointer { frames -> [String] in
            guard let base = frames.baseAddress, let lines = backtrace_symbols(base, Int32(count)) else { return [] }
            defer { free(lines) }
            return (0..<count).compactMap { index in
                guard let line = lines[index] else { return nil as String? }
                return String(cString: line)
            }
        }
        perfMainThreadCaptureState.store(0, ordering: .releasing)
        return symbols.isEmpty ? ["main-thread backtrace unavailable"] : Array(symbols.prefix(32))
    }

    private func record(_ block: MainThreadBlock, generation: Int) {
        lock.lock()
        guard worker != nil, self.generation == generation else { lock.unlock(); return }
        count += 1
        maxMS = max(maxMS, block.durationMS)
        longest.append(block)
        longest.sort { $0.durationMS > $1.durationMS }
        longest = Array(longest.prefix(Self.retainedBlocks))
        lock.unlock()
        PerfTrace.event("Main thread block", String(format: "%.1f ms\n%@", block.durationMS, block.backtrace.joined(separator: "\n")))
    }
}
#endif
