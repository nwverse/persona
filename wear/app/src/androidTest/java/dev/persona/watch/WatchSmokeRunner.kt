package dev.persona.watch

import android.app.Activity
import android.app.Instrumentation
import android.content.pm.PackageManager
import android.os.Bundle
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubException
import java.io.File

/** Runs the production transport and credential store on the watch, without sending or approving anything. */
class WatchSmokeRunner : Instrumentation() {
    private var arguments = Bundle()
    override fun onCreate(arguments: Bundle?) {
        super.onCreate(arguments)
        this.arguments = arguments ?: Bundle()
        start()
    }

    override fun onStart() {
        val result = Bundle()
        try {
            if (arguments.getString("mode") == "fixture") {
                finish(Activity.RESULT_OK, FixtureUiChecks(this).run(arguments.getString("scenario")))
                return
            }
            if (arguments.getString("mode") == "multihub") {
                finish(Activity.RESULT_OK, MultiHubChecks(this).run(checkNotNull(arguments.getString("secondary_url"))))
                return
            }
            if (arguments.getString("mode") == "surfaces") {
                finish(Activity.RESULT_OK, SurfaceUiChecks(this).run())
                return
            }
            if (arguments.getString("mode") == "pet_expiry") {
                finish(Activity.RESULT_OK, PetExpiryChecks(this).run())
                return
            }
            if (arguments.getString("mode") == "pet_expiry_restore") {
                finish(Activity.RESULT_OK, PetExpiryChecks(this).restore())
                return
            }
            if (arguments.getString("mode") == "complication_parcel") {
                finish(Activity.RESULT_OK, ComplicationParcelChecks(this).run())
                return
            }
            if (arguments.getString("mode") == "real_surfaces") {
                finish(Activity.RESULT_OK, LiveSurfaceChecks(this).run())
                return
            }
            if (arguments.getString("mode") == "live_ping") {
                val session = checkNotNull(arguments.getString("session_id")) { "test_session_required" }
                finish(Activity.RESULT_OK, LivePingChecks(this).run(session))
                return
            }
            check(targetContext.packageManager.hasSystemFeature(PackageManager.FEATURE_WATCH)) { "Not a watch" }
            val config = checkNotNull(CredentialStore(targetContext).load()) { "Watch is not paired" }
            val envelope = File(targetContext.noBackupFilesDir, "connection.enc").readText()
            check(!envelope.contains(config.token)) { "Credential is not encrypted" }
            val start = System.nanoTime()
            val client = HubClient(config)
            val snapshot = client.refresh()
            check(snapshot.machines.isNotEmpty()) { "No connected computers returned" }
            val session = snapshot.sessions.firstOrNull { it.assistant } ?: snapshot.sessions.firstOrNull()
            val messages = session?.let { client.messages(it) }.orEmpty()
            result.putString("stream", "PASS: real-device credential encryption, authenticated refresh and transcript retrieval\n")
            result.putInt("sessions", snapshot.sessions.size)
            result.putInt("interactions", snapshot.interactions.size)
            result.putInt("machines", snapshot.machines.size)
            result.putInt("inbox", snapshot.inbox.size)
            result.putInt("messages", messages.size)
            result.putBoolean("tls", config.baseUrl.startsWith("https://"))
            result.putBoolean("pinned", config.fingerprint != null)
            result.putLong("elapsed_ms", (System.nanoTime() - start) / 1_000_000)
            finish(Activity.RESULT_OK, result)
        } catch (failure: Throwable) {
            // Only a fixed exception type is reported; URLs, tokens and private messages stay on device.
            result.putString("stream", "FAIL: ${(failure as? HubException)?.code ?: failure.javaClass.simpleName}\n")
            if (arguments.getString("mode") == "fixture") {
                result.putString("fixture_failure", failure.message)
                result.putString("fixture_cause", failure.cause?.message)
            }
            if (arguments.getString("mode") == "multihub") {
                result.putString("fixture_failure", failure.message)
                result.putString("fixture_cause", failure.cause?.message)
            }
            if (arguments.getString("mode") == "surfaces") result.putString("fixture_failure", failure.message)
            if (arguments.getString("mode") == "live_ping") result.putString("ping_failure", failure.message)
            finish(Activity.RESULT_CANCELED, result)
        }
    }
}
