package persona.core.contracts.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import persona.core.contracts.SessionKey

/** The default assistant's private space on the pairing that answered the health list. */
interface HealthSpace {
    /** Qualified for this pairing in the app's merged machine list. */
    val machineId: String
    suspend fun reports(): List<CheckupReport>
    suspend fun deleteReport(name: String)
    suspend fun memory(): List<MemoryFile>
    suspend fun saveMemory(name: String, content: String)
    suspend fun deleteMemory(name: String)
    /** All selected originals have already been uploaded to this machine with dest=health. */
    suspend fun startReportChat(prompt: String): SessionKey
}

@Serializable
data class CheckupReport(
    val name: String, val title: String, val path: String? = null,
    @SerialName("record_path") val recordPath: String? = null,
    @SerialName("modified_at") val modifiedAt: String? = null,
)

/** Health Connect reads belong to this phone, behind the Health switch for the current pairing. */
interface PhoneHealthRecords {
    val changes: kotlinx.coroutines.flow.Flow<Unit>
    suspend fun trends(days: Int): HealthTrends
    suspend fun workouts(): List<HealthWorkout>
    suspend fun workout(id: String): HealthWorkout
}

enum class HealthReadProblem { Disabled, Permission, Unavailable, HubDenied, HubOutdated }
class HealthReadException(val problem: HealthReadProblem) : Exception()

data class HealthDay(val date: String, val steps: Double?, val sleep: Double?, val weight: Double?)
data class HealthTrends(val days: List<HealthDay>, val notGranted: Set<String>, val failed: Set<String>)

@Serializable data class HealthWorkout(
    val id: String, val activity: String, val start: String, val duration: Double,
    val distance: Double? = null, val energy: Double? = null,
    @SerialName("heart_rate_avg") val averageHeartRate: Double? = null,
    @SerialName("heart_rate_max") val maxHeartRate: Double? = null,
    @SerialName("elevation_gain") val elevation: Double? = null,
    val splits: List<HealthSplit> = emptyList(),
    @SerialName("heart_rate") val heartRate: List<HealthHeartRate> = emptyList(),
)

@Serializable data class HealthSplit(val km: Int, val seconds: Double)
@Serializable data class HealthHeartRate(val minute: Int, val bpm: Double)
