package dev.persona.watch

import android.app.Instrumentation
import android.os.Bundle
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.UrlConnectionTransport
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import java.util.UUID

/** Actual Keystore loss in a disposable namespace; the production credential alias is never removed. */
internal class CredentialLossChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext

    fun run(): Bundle {
        check(BuildConfig.DEBUG) { "credential_loss_debug_required" }
        val config = checkNotNull(CredentialStore(context).load()) { "credential_loss_fixture_required" }.validated()
        val isolation = FixtureIsolation(config, FIXTURE_TOKEN)
        isolation.verify(config, HubDirectory(context).additional())
        // This shares the fixture authority/port guard before even assigning its synthetic token.
        FixtureControlOrigin.parse(config.baseUrl).url("/__test")
        val response = UrlConnectionTransport().request(config, "GET", "/api/v1/health", null, emptyMap())
        check(response.status == 200 && JSONObject(response.body).optBoolean("fixture")) { "credential_loss_fixture_health" }
        val originalEnvelope = File(context.noBackupFilesDir, "connection.enc").readBytes()
        val id = UUID.randomUUID().toString()
        val alias = "persona.watch.fixture.loss.$id"
        val directory = File(context.noBackupFilesDir, "credential-loss-$id").canonicalFile
        check(directory.parentFile == context.noBackupFilesDir.canonicalFile && directory.mkdir()) { "credential_loss_private_directory" }
        var keys: KeyStore? = null
        var ownedAlias = false
        var primaryFailure: Throwable? = null
        try {
            val currentKeys = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            keys = currentKeys
            check(!currentKeys.containsAlias(alias)) { "credential_loss_alias_collision" }
            ownedAlias = true
            val store = CredentialStore.isolatedForCheck(context, directory, alias)
            val fixture = HubConfig(config.baseUrl, FIXTURE_TOKEN, deviceId = "fixture-credential-loss")
            store.save(fixture)
            check(store.load() == fixture && currentKeys.containsAlias(alias)) { "credential_loss_initial_round_trip" }
            val envelope = File(directory, "connection.enc").readBytes()
            check(!String(envelope, Charsets.UTF_8).contains(FIXTURE_TOKEN)) { "credential_loss_plaintext" }
            currentKeys.deleteEntry(alias)
            check(!currentKeys.containsAlias(alias)) { "credential_loss_key_not_removed" }
            check(store.exists() && store.load() == null) { "credential_loss_not_fail_closed" }
            check(File(directory, "connection.enc").readBytes().contentEquals(envelope)) { "credential_loss_envelope_changed" }
            val replacement = fixture.copy(token = "persona-wear-fixture-recovered", deviceId = "fixture-credential-recovered")
            store.save(replacement)
            check(CredentialStore.isolatedForCheck(context, directory, alias).load() == replacement) { "credential_loss_recovery_failed" }
            isolation.verify(CredentialStore(context).load(), HubDirectory(context).additional())
            check(File(context.noBackupFilesDir, "connection.enc").readBytes().contentEquals(originalEnvelope)) { "credential_loss_primary_changed" }
            return Bundle().apply {
                putBoolean("credential_loss_passed", true)
                putBoolean("production_alias_untouched", true)
                putString("stream", "PASS: actual Android Keystore encryption, isolated key loss fails closed, and replacement credential decrypts after recovery; production alias/envelope unchanged.\n")
            }
        } catch (failure: Throwable) {
            primaryFailure = failure
            throw failure
        } finally {
            var cleanupFailure: Throwable? = null
            fun cleanup(action: () -> Unit) {
                try { action() } catch (failure: Throwable) {
                    val previous = cleanupFailure
                    if (previous == null) cleanupFailure = failure else previous.addSuppressed(failure)
                }
            }
            if (ownedAlias) cleanup {
                val currentKeys = checkNotNull(keys)
                currentKeys.deleteEntry(alias)
                check(!currentKeys.containsAlias(alias)) { "credential_loss_alias_cleanup_failed" }
            }
            cleanup { check(directory.deleteRecursively()) { "credential_loss_file_cleanup_failed" } }
            cleanupFailure?.let { failure ->
                val original = primaryFailure
                if (original != null) original.addSuppressed(failure) else throw failure
            }
        }
    }

    companion object { private const val FIXTURE_TOKEN = "persona-wear-fixture-only" }
}
