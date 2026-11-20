package persona.platform.phonetools

import android.content.Context
import androidx.health.connect.client.permission.HealthPermission
import androidx.health.connect.client.records.ExerciseSessionRecord
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.serialization.json.*
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*

internal class LocalHealthRecords(
    private val context: Context,
    private val enabled: StateFlow<Set<PhoneSource>>,
    revisions: StateFlow<Int>,
) : PhoneHealthRecords {
    override val changes: Flow<Unit> = combine(enabled, revisions) { _, _ -> Unit }

    private suspend fun <T> read(required: Set<String>, observed: Set<String> = required, operation: suspend () -> T): T {
        suspend fun grants(): Set<String> {
            if (PhoneSource.Health !in enabled.value) throw HealthReadException(HealthReadProblem.Disabled)
            if (!Health.available(context)) throw HealthReadException(HealthReadProblem.Unavailable)
            val granted = Health.granted(context)
            if (granted.none { it in required }) throw HealthReadException(HealthReadProblem.Permission)
            return granted.intersect(observed)
        }
        val before = grants()
        return try { operation().also { if (!grants().containsAll(before)) throw HealthReadException(HealthReadProblem.Permission) } }
        catch (e: SecurityException) { throw HealthReadException(HealthReadProblem.Permission) }
    }

    override suspend fun trends(days: Int): HealthTrends {
        require(days == 7 || days == 30)
        val metrics = listOf("steps", "sleep", "weight")
        return read(metrics.map { HealthPermission.getReadPermission(Health.recordTypes.getValue(it)) }.toSet()) {
            healthTrends(Health.summaryData(context, buildJsonObject {
                put("days", days); putJsonArray("metrics") { metrics.forEach { add(it) } }
            }))
        }
    }

    private val workoutsPermission = setOf(HealthPermission.getReadPermission(ExerciseSessionRecord::class))
    private val workoutReads = Workouts.permissions + Health.recordTypes.values.map { HealthPermission.getReadPermission(it) }
    override suspend fun workouts(): List<HealthWorkout> = read(workoutsPermission, workoutReads) {
        JsonCodec.decodeFromJsonElement(Workouts.recentData(context, buildJsonObject { }).getValue("workouts"))
    }
    override suspend fun workout(id: String): HealthWorkout = read(workoutsPermission, workoutReads) {
        JsonCodec.decodeFromJsonElement(Workouts.detailData(context, buildJsonObject { put("id", id) }))
    }
}

/** A missing measurement or a failed read is never drawn as zero. */
internal fun healthTrends(result: JsonObject): HealthTrends {
    val failed = mutableSetOf<String>()
    val rows = result.getValue("data").jsonArray.map { item ->
        val row = item.jsonObject
        fun value(key: String): Double? {
            val raw = row[key]
            if (raw is JsonObject && "error" in raw) failed += key
            return (raw as? JsonPrimitive)?.doubleOrNull?.takeIf { it.isFinite() }
        }
        HealthDay(row.getValue("date").jsonPrimitive.content, value("steps"), value("sleep"), value("weight"))
    }
    val refused = (result["not_granted"] as? JsonArray).orEmpty().map { it.jsonPrimitive.content }.toSet()
    return HealthTrends(rows, refused, failed)
}
