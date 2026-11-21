package dev.persona.watch

import android.app.Instrumentation
import android.os.Bundle
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubConfig
import dev.persona.watch.pet.PetBridge
import dev.persona.watch.pet.PetPack
import dev.persona.watch.pet.PetState
import dev.persona.watch.pet.PetStatus
import dev.persona.watch.pet.PetStoredStatus
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.security.MessageDigest

/** Seeds only verified synthetic cached state; the operator observes already configured face slots. */
class PetExpiryChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext
    private val backup get() = File(context.filesDir, BACKUP_FILE)

    fun run(): Bundle {
        val config = fixtureConnection()
        check(!backup.exists()) { "pet_expiry_restore_previous_run_first" }
        verifyFixture(config)
        check(PetPack(context.cacheDir, config).cached(verify = true)?.assets?.size == 13) {
            "pet_expiry_verified_assets_required"
        }
        sameConnection(config)
        val previous = PetBridge.read(context, config)
        val saved = JSONObject().put("version", 1).put("fixture", true).put("identity", identity(config))
            .put("state", previous.status.state.wire).put("count", previous.status.count).put("updated_at", previous.updatedAt)
        check(backup.createNewFile()) { "pet_expiry_backup_exists" }
        try {
            FileOutputStream(backup).use { output ->
                output.write(saved.toString().toByteArray(Charsets.UTF_8))
                output.fd.sync()
            }
        } catch (failure: Throwable) {
            backup.delete()
            throw failure
        }

        // Keep the backup until explicit restore: observation happens after this runner has finished.
        sameConnection(config)
        val expires = System.currentTimeMillis() + OBSERVATION_WINDOW_MS
        val seeded = PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 42), expires - PetStoredStatus.STALE_MS)
        PetBridge.publishDurably(context, config, seeded.status, seeded.updatedAt)
        check(PetBridge.read(context, config) == seeded) { "pet_expiry_seed_not_saved" }
        return Bundle().apply {
            putBoolean("pet_expiry_seeded", true)
            putInt("pet_expiry_count", 42)
            putLong("expires_at_ms", expires)
            putLong("observation_window_ms", OBSERVATION_WINDOW_MS)
            putString("stream", "SEEDED: synthetic Pet status 42; observe existing face slots until Offline, then restore. No face selection or expiry observation is claimed.\n")
        }
    }

    /** Cleanup works with a stopped fixture server, but never restores into a changed connection. */
    fun restore(): Bundle {
        val config = fixtureConnection()
        check(backup.isFile && backup.length() in 1..4096) { "pet_expiry_backup_required" }
        val saved = JSONObject(backup.readText(Charsets.UTF_8))
        check(saved.optInt("version") == 1 && saved.optBoolean("fixture") && saved.optString("identity") == identity(config)) {
            "pet_expiry_backup_connection_mismatch"
        }
        val state = checkNotNull(PetState.entries.firstOrNull { it.wire == saved.optString("state") }) { "pet_expiry_invalid_backup" }
        val count = saved.getInt("count")
        check(count >= 0) { "pet_expiry_invalid_backup" }
        val previous = PetStoredStatus(PetStatus(state, count), saved.getLong("updated_at"))
        sameConnection(config)
        val restored = PetBridge.publishDurably(context, config, previous.status, previous.updatedAt)
        check(PetBridge.read(context, config) == restored) { "pet_expiry_restore_not_saved" }
        check(backup.delete()) { "pet_expiry_backup_cleanup_failed" }
        return Bundle().apply {
            putBoolean("pet_expiry_restored", true)
            putString("stream", "RESTORED: original synthetic Pet cache; test backup removed.\n")
        }
    }

    private fun fixtureConnection(): HubConfig {
        check(BuildConfig.DEBUG) { "pet_expiry_requires_debug" }
        val config = checkNotNull(CredentialStore(context).load()) { "pet_expiry_connection_required" }.validated()
        val uri = URI(config.baseUrl)
        check(config.token == FIXTURE_TOKEN && uri.scheme == "http" && uri.host in setOf("127.0.0.1", "localhost", "::1", "[::1]") &&
            uri.path.orEmpty() in setOf("", "/") && uri.userInfo == null && uri.query == null && uri.fragment == null) {
            "pet_expiry_loopback_fixture_required"
        }
        check(HubDirectory(context).additional().isEmpty()) { "pet_expiry_existing_groups_untouched" }
        return config
    }

    private fun sameConnection(config: HubConfig) {
        check(fixtureConnection() == config) { "pet_expiry_connection_changed" }
    }

    private fun verifyFixture(config: HubConfig) {
        val connection = URL(config.baseUrl + "/api/v1/health").openConnection() as HttpURLConnection
        try {
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 2_000
            connection.readTimeout = 3_000
            connection.setRequestProperty("Authorization", "Bearer $FIXTURE_TOKEN")
            check(connection.responseCode == 200) { "pet_expiry_health_unavailable" }
            val health = JSONObject(connection.inputStream.bufferedReader(Charsets.UTF_8).use { it.readText() })
            check(health.optBoolean("fixture") && health.optString("instance").isNotBlank()) { "pet_expiry_fixture_marker_required" }
        } finally { connection.disconnect() }
        val snapshot = HubClient(config, "en").refresh()
        check(snapshot.machines.isNotEmpty() && snapshot.machines.all { it.id == "fixture" } &&
            snapshot.sessions.isNotEmpty() && snapshot.sessions.all { it.machineId == "fixture" }) { "pet_expiry_fixture_data_required" }
        sameConnection(config)
    }

    private fun identity(config: HubConfig): String = MessageDigest.getInstance("SHA-256")
        .digest(listOf(config.baseUrl, config.token, config.fingerprint.orEmpty(), config.deviceId.orEmpty()).joinToString("\u0000").toByteArray(Charsets.UTF_8))
        .joinToString("") { "%02x".format(it) }

    companion object {
        private const val FIXTURE_TOKEN = "persona-wear-fixture-only"
        private const val BACKUP_FILE = "pet-expiry-check.json"
        private const val OBSERVATION_WINDOW_MS = 45_000L
    }
}
