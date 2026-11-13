#if os(iOS)
import Foundation
import HealthKit

/// Health signals used to decide which rolling-recording segments to keep. Read-only: nothing is ever written.
enum RecordingHealthSignals {
    static let store = HKHealthStore()
    private static let day: TimeInterval = 86_400

    static var readTypes: Set<HKObjectType> {
        var types: Set<HKObjectType> = []
        for identifier in [HKQuantityTypeIdentifier.heartRate, .restingHeartRate, .heartRateVariabilitySDNN, .stepCount] {
            if let type = HKQuantityType.quantityType(forIdentifier: identifier) { types.insert(type) }
        }
        types.insert(HKObjectType.workoutType())
        types.insert(HKObjectType.stateOfMindType())
        return types
    }

    static func requestAccess() async {
        guard HKHealthStore.isHealthDataAvailable() else { return }
        _ = try? await store.requestAuthorization(toShare: Set<HKSampleType>(), read: readTypes)
    }

    /// Errors (for example protected data while the phone is locked) are thrown so the caller can fall back.
    static func fetch(from: Date, to: Date) async throws -> RecordingSignals {
        guard HKHealthStore.isHealthDataAvailable() else { return RecordingSignals() }
        var signals = RecordingSignals()
        let window = HKQuery.predicateForSamples(withStart: from, end: to, options: [])
        let bpm = HKUnit.count().unitDivided(by: .minute())
        let milliseconds = HKUnit.secondUnit(with: .milli)

        signals.heartRate = try await samples(.heartRate, window).map {
            RecordingSignals.Sample(at: $0.startDate, value: $0.quantity.doubleValue(for: bpm))
        }

        // The most recent resting heart rate in the last week; samples come back oldest first.
        let restingWindow = HKQuery.predicateForSamples(withStart: to.addingTimeInterval(-7 * day), end: to, options: [])
        signals.restingHeartRate = try await samples(.restingHeartRate, restingWindow).last.map {
            $0.quantity.doubleValue(for: bpm)
        }

        signals.hrv = try await samples(.heartRateVariabilitySDNN, window).map {
            RecordingSignals.Sample(at: $0.startDate, value: $0.quantity.doubleValue(for: milliseconds))
        }
        let monthWindow = HKQuery.predicateForSamples(withStart: to.addingTimeInterval(-30 * day), end: to, options: [])
        let monthly = try await samples(.heartRateVariabilitySDNN, monthWindow)
        signals.hrvMedian = median(monthly.map { $0.quantity.doubleValue(for: milliseconds) })

        signals.steps = try await steps(from: from, window)

        let workoutPredicate: HKSamplePredicate<HKWorkout> = .workout(window)
        let workouts = try await HKSampleQueryDescriptor(
            predicates: [workoutPredicate],
            sortDescriptors: [SortDescriptor(\HKWorkout.startDate)]
        ).result(for: store)
        signals.workouts = workouts.map { RecordingSignals.Span(from: $0.startDate, to: $0.endDate, value: 0) }

        let moodPredicate: HKSamplePredicate<HKStateOfMind> = .stateOfMind(window)
        let moods = try await HKSampleQueryDescriptor(
            predicates: [moodPredicate],
            sortDescriptors: [SortDescriptor(\HKStateOfMind.startDate)]
        ).result(for: store)
        signals.moods = moods.map(\.startDate)

        return signals
    }

    private static func samples(_ identifier: HKQuantityTypeIdentifier, _ window: NSPredicate) async throws -> [HKQuantitySample] {
        guard let type = HKQuantityType.quantityType(forIdentifier: identifier) else { return [] }
        let predicate: HKSamplePredicate<HKQuantitySample> = .quantitySample(type: type, predicate: window)
        return try await HKSampleQueryDescriptor(
            predicates: [predicate],
            sortDescriptors: [SortDescriptor(\HKQuantitySample.startDate)]
        ).result(for: store)
    }

    /// Steps per 5 minutes. A cumulative sum lets HealthKit merge the phone's and the watch's counts of the same steps;
    /// adding the raw samples counts them twice.
    private static func steps(from: Date, _ window: NSPredicate) async throws -> [RecordingSignals.Span] {
        guard let type = HKQuantityType.quantityType(forIdentifier: .stepCount) else { return [] }
        let interval = DateComponents(minute: 5)
        let descriptor = HKStatisticsCollectionQueryDescriptor(
            predicate: .quantitySample(type: type, predicate: window),
            options: .cumulativeSum,
            anchorDate: Calendar.current.startOfDay(for: from),
            intervalComponents: interval
        )
        let collection = try await descriptor.result(for: store)
        let count = HKUnit.count()
        return collection.statistics().compactMap { statistics in
            guard let sum = statistics.sumQuantity() else { return nil }
            return RecordingSignals.Span(from: statistics.startDate, to: statistics.endDate, value: sum.doubleValue(for: count))
        }
    }

    private static func median(_ values: [Double]) -> Double? {
        let sorted = values.sorted()
        guard !sorted.isEmpty else { return nil }
        let middle = sorted.count / 2
        return sorted.count % 2 == 0 ? (sorted[middle - 1] + sorted[middle]) / 2 : sorted[middle]
    }
}
#endif
