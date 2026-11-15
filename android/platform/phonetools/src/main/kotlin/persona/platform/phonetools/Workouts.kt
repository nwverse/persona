package persona.platform.phonetools

import android.content.Context
import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.aggregate.AggregateMetric
import androidx.health.connect.client.aggregate.AggregationResult
import androidx.health.connect.client.permission.HealthPermission
import androidx.health.connect.client.records.ActiveCaloriesBurnedRecord
import androidx.health.connect.client.records.DistanceRecord
import androidx.health.connect.client.records.ElevationGainedRecord
import androidx.health.connect.client.records.ExerciseSegment
import androidx.health.connect.client.records.ExerciseSessionRecord
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.request.AggregateRequest
import androidx.health.connect.client.request.ReadRecordsRequest
import androidx.health.connect.client.time.TimeRangeFilter
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import persona.core.contracts.api.PhoneToolResult
import java.time.Duration
import java.time.Instant
import kotlin.math.floor

/**
 * `phone_workouts_recent` and `phone_workout_detail` (iOS `WorkoutTool`) on Health Connect: exercise sessions, read
 * only. A session's numbers are the ones Health Connect holds in the session's own time window, so a workout from
 * another app shows the same way as the phone's own recordings. No route or location is read.
 */
object Workouts {
    /** The schema's activity names (lowercase, `_` for spaces) and the Health Connect exercise types each covers. */
    val activities: Map<String, List<Int>> = linkedMapOf(
        "cycling" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_BIKING, ExerciseSessionRecord.EXERCISE_TYPE_BIKING_STATIONARY),
        "hiking" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_HIKING),
        "interval_training" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING),
        "running" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_RUNNING, ExerciseSessionRecord.EXERCISE_TYPE_RUNNING_TREADMILL),
        "strength_training" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_STRENGTH_TRAINING),
        "swimming" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_SWIMMING_POOL, ExerciseSessionRecord.EXERCISE_TYPE_SWIMMING_OPEN_WATER),
        "walking" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_WALKING),
        "yoga" to listOf(ExerciseSessionRecord.EXERCISE_TYPE_YOGA),
    )

    /** The read permissions workouts need on top of the Health summary's (Settings › Phone data › Health asks for both). */
    val permissions: Set<String> = listOf(
        ExerciseSessionRecord::class, ElevationGainedRecord::class,
    ).map { HealthPermission.getReadPermission(it) }.toSet()

    private val exercisePermission = HealthPermission.getReadPermission(ExerciseSessionRecord::class)

    /** Activities whose distance is counted in whole kilometres (iOS `WorkoutReader.distanceType` and the pace rule). */
    private val byTheKilometre = setOf("running", "walking", "hiking", "cycling")

    /** iOS `WorkoutReader.activityName`: the words a report and the tools use. */
    private fun activityName(type: Int): String = when (type) {
        ExerciseSessionRecord.EXERCISE_TYPE_RUNNING, ExerciseSessionRecord.EXERCISE_TYPE_RUNNING_TREADMILL -> "Running"
        ExerciseSessionRecord.EXERCISE_TYPE_WALKING -> "Walking"
        ExerciseSessionRecord.EXERCISE_TYPE_BIKING, ExerciseSessionRecord.EXERCISE_TYPE_BIKING_STATIONARY -> "Cycling"
        ExerciseSessionRecord.EXERCISE_TYPE_SWIMMING_POOL, ExerciseSessionRecord.EXERCISE_TYPE_SWIMMING_OPEN_WATER -> "Swimming"
        ExerciseSessionRecord.EXERCISE_TYPE_HIKING -> "Hiking"
        ExerciseSessionRecord.EXERCISE_TYPE_YOGA -> "Yoga"
        ExerciseSessionRecord.EXERCISE_TYPE_STRENGTH_TRAINING -> "Strength training"
        ExerciseSessionRecord.EXERCISE_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING -> "Interval training"
        else -> "Workout (Health Connect exercise type $type)"  // i18n-ignore: tool result for the model
    }

    /** The activity key a session's type belongs to, or null for the types the schema does not filter by. */
    private fun activityKey(type: Int): String? = activities.entries.firstOrNull { type in it.value }?.key

    /** Health Connect's missing or outdated provider, or the exercise permission not granted, as a tool error. */
    private suspend fun client(c: Context): HealthConnectClient {
        Health.modelReason(c)?.let {
            unavailable("$it，读不了运动数据。\nHealth Connect is not available on this Android phone, so there are no workouts to read.")  // i18n-ignore: tool result for the model
        }
        val client = HealthConnectClient.getOrCreate(c)
        if (exercisePermission !in client.permissionController.getGrantedPermissions()) {
            unavailable("没有权限：健康 / Permission denied: health.\n" + PhoneToolText.grantHint(PhoneSource.Health))  // i18n-ignore: tool result for the model
        }
        return client
    }

    suspend fun recent(c: Context, args: JsonObject): PhoneToolResult {
        val client = client(c)
        val types = args.raw("activity")?.trim()?.takeIf { it.isNotEmpty() }?.let { name ->
            val key = name.lowercase().replace(' ', '_')
            activities[key] ?: invalid("unknown activity \"$name\"; use one of ${activities.keys.sorted().joinToString(", ")}")
        }
        val days = args.int("days", 30, 1, 365)
        val limit = args.int("limit", 20, 1, 50)
        val now = Instant.now()
        val sessions = client.readRecords(
            ReadRecordsRequest(ExerciseSessionRecord::class, TimeRangeFilter.between(now.minus(Duration.ofDays(days.toLong())), now),
                ascendingOrder = false, pageSize = 500),
        ).records
        val rows = sessions
            .filter { types == null || it.exerciseType in types }
            .take(limit)
            .map { row(c, client, it) }
        return PhoneToolResult.text(buildJsonObject {
            put("workouts", buildJsonArray { rows.forEach { add(it) } })
        }.toString())
    }

    suspend fun detail(c: Context, args: JsonObject): PhoneToolResult {
        val client = client(c)
        val id = args.raw("id")?.trim()?.takeIf { it.isNotEmpty() } ?: invalid("id must be a workout's id from phone_workouts_recent")
        val session = try {
            client.readRecord(ExerciseSessionRecord::class, id).record
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            unavailable("No workout with id $id in Health Connect (deleted, or Persona may not read it). ${e.message.orEmpty()}")
        }
        val fields = row(c, client, session).toMutableMap()
        val splits = splits(client, session)
        if (splits.isNotEmpty()) {
            fields["splits"] = buildJsonArray {
                splits.forEach { add(buildJsonObject { put("km", it.km); put("seconds", it.seconds) }) }
            }
        }
        val rates = heartRates(client, session)
        if (rates.isNotEmpty()) {
            fields["heart_rate"] = buildJsonArray {
                rates.forEach { add(buildJsonObject { put("minute", it.first); put("bpm", it.second) }) }
            }
        }
        return PhoneToolResult.text(JsonObject(fields).toString())
    }

    /** One session's row; fields without data are left out (aggregates that fail count as no data). */
    private suspend fun row(c: Context, client: HealthConnectClient, s: ExerciseSessionRecord): JsonObject {
        val totals = totals(client, s)
        val seconds = Duration.between(s.startTime, s.endTime).seconds.toDouble()
        val distance = totals?.get(DistanceRecord.DISTANCE_TOTAL)?.inMeters
        val key = activityKey(s.exerciseType)
        val pace = if (distance != null && distance >= 500 && key in byTheKilometre) Math.round(seconds / (distance / 1000)).toDouble() else null
        val energy = totals?.get(ActiveCaloriesBurnedRecord.ACTIVE_CALORIES_TOTAL)?.inKilocalories
        val gain = totals?.get(ElevationGainedRecord.ELEVATION_GAINED_TOTAL)?.inMeters?.takeIf { it.isFinite() && it >= 0 && it <= 20000 }
        val avg = totals?.get(HeartRateRecord.BPM_AVG)?.toDouble()
        val max = totals?.get(HeartRateRecord.BPM_MAX)?.toDouble()
        // iOS keeps the pair only when it makes sense together.
        val rate = if (avg != null && max != null && avg > 0 && max <= 300 && avg <= max) avg to max else null
        val out = linkedMapOf<String, JsonElement>(
            "id" to JsonPrimitive(s.metadata.id),
            "activity" to JsonPrimitive(activityName(s.exerciseType)),
            "source" to JsonPrimitive(sourceName(c, s.metadata.dataOrigin.packageName)),
            "start" to JsonPrimitive(iso(s.startTime)),
            "end" to JsonPrimitive(iso(s.endTime)),
            "duration" to JsonPrimitive(seconds),
        )
        distance?.let { out["distance"] = JsonPrimitive(Math.round(it).toDouble()) }
        energy?.let { out["energy"] = JsonPrimitive(Math.round(it).toDouble()) }
        gain?.let { out["elevation_gain"] = JsonPrimitive(Math.round(it).toDouble()) }
        rate?.let {
            out["heart_rate_avg"] = JsonPrimitive(Math.round(it.first).toDouble())
            out["heart_rate_max"] = JsonPrimitive(Math.round(it.second).toDouble())
        }
        pace?.let { out["pace"] = JsonPrimitive(it) }
        return JsonObject(out)
    }

    private fun sourceName(c: Context, packageName: String): String = runCatching {
        val pm = c.packageManager
        pm.getApplicationLabel(pm.getApplicationInfo(packageName, 0)).toString()
    }.getOrDefault(packageName)

    /** Totals over the session's window; null when the aggregate fails (the fields are then left out). */
    private suspend fun totals(client: HealthConnectClient, s: ExerciseSessionRecord): AggregationResult? = try {
        client.aggregate(
            AggregateRequest(
                setOf<AggregateMetric<*>>(
                    DistanceRecord.DISTANCE_TOTAL, ActiveCaloriesBurnedRecord.ACTIVE_CALORIES_TOTAL,
                    ElevationGainedRecord.ELEVATION_GAINED_TOTAL, HeartRateRecord.BPM_AVG, HeartRateRecord.BPM_MAX,
                ),
                TimeRangeFilter.between(s.startTime, s.endTime),
            ),
        )
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        null
    }

    /** Whole kilometres of a run, walk, hike or ride, pauses left out (iOS `WorkoutReader.splits`). */
    private suspend fun splits(client: HealthConnectClient, s: ExerciseSessionRecord): List<WorkoutSplits.Split> {
        val key = activityKey(s.exerciseType)
        if (key !in byTheKilometre) return emptyList()
        val records = client.readRecords(
            ReadRecordsRequest(DistanceRecord::class, TimeRangeFilter.between(s.startTime, s.endTime), ascendingOrder = true, pageSize = 1000),
        ).records
        val samples = records.map { WorkoutSplits.Sample(it.startTime, it.endTime, it.distance.inMeters) }
        val paused = s.segments
            .filter { it.segmentType == ExerciseSegment.EXERCISE_SEGMENT_TYPE_PAUSE }
            .map { it.startTime to it.endTime }
        return WorkoutSplits.of(samples, s.startTime, s.endTime, paused)
    }

    /** The average bpm of each minute from the start, as (minute, bpm) pairs (iOS `WorkoutReader.heartRates`). */
    private suspend fun heartRates(client: HealthConnectClient, s: ExerciseSessionRecord): List<Pair<Int, Double>> {
        val records = client.readRecords(
            ReadRecordsRequest(HeartRateRecord::class, TimeRangeFilter.between(s.startTime, s.endTime), ascendingOrder = true, pageSize = 1000),
        ).records
        val last = Duration.between(s.startTime, s.endTime).toMinutes().toInt()
        val buckets = sortedMapOf<Int, MutableList<Double>>()
        for (record in records) for (sample in record.samples) {
            val minute = floor(Duration.between(s.startTime, sample.time).toMillis() / 60000.0).toInt()
            val bpm = sample.beatsPerMinute.toDouble()
            if (minute in 0..last && bpm > 0 && bpm <= 300) buckets.getOrPut(minute) { mutableListOf() }.add(bpm)
        }
        return buckets.map { (minute, values) -> minute to Math.round(values.average()).toDouble() }
    }
}

/** The split arithmetic, a port of iOS `WorkoutSplits` (kept apart so it can be tested without Health Connect). */
internal object WorkoutSplits {
    data class Sample(val start: Instant, val end: Instant, val meters: Double)
    data class Split(val km: Int, val seconds: Double)

    /** At most this many: the hub refuses more. */
    const val LIMIT = 1000

    /**
     * Whole kilometres in order; the last part kilometre is left out. A sample that cannot give a sensible split (no
     * time, or outside the workout) leaves no splits at all: a wrong pace is worse than none.
     */
    fun of(samples: List<Sample>, start: Instant, end: Instant, paused: List<Pair<Instant, Instant>>): List<Split> {
        val out = mutableListOf<Split>()
        var covered = 0.0
        var mark = start
        for (s in samples.sortedBy { it.start }) {
            if (s.meters <= 0) continue
            if (s.start < start || s.end < s.start || s.end > end) return emptyList()
            val before = covered
            covered += s.meters
            while (out.size < LIMIT && covered >= (out.size + 1) * 1000.0) {
                val share = ((out.size + 1) * 1000.0 - before) / s.meters
                val span = Duration.between(s.start, s.end).toNanos()
                val at = maxOf(mark, s.start.plusNanos((span * share).toLong()))
                val seconds = Math.round(active(mark, at, paused) * 10) / 10.0
                if (seconds < 1) return emptyList()
                out += Split(out.size + 1, seconds)
                mark = at
            }
        }
        return out
    }

    /** Seconds from [a] to [b] outside the pauses. */
    fun active(a: Instant, b: Instant, paused: List<Pair<Instant, Instant>>): Double {
        if (b <= a) return 0.0
        var time = Duration.between(a, b).toNanos() / 1e9
        for ((ps, pe) in paused) {
            val lo = maxOf(a, ps)
            val hi = minOf(b, pe)
            if (hi > lo) time -= Duration.between(lo, hi).toNanos() / 1e9
        }
        return maxOf(time, 0.0)
    }
}
