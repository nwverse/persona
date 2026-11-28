package dev.persona.watch

import android.app.Instrumentation
import android.os.Bundle
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubException
import dev.persona.watch.data.HubTransport
import dev.persona.watch.data.UrlConnectionTransport
import dev.persona.watch.extensions.ExtensionClient
import dev.persona.watch.extensions.ExtensionComplications
import dev.persona.watch.extensions.ExtensionException
import dev.persona.watch.extensions.ExtensionCacheStore
import dev.persona.watch.extensions.WidgetFaceKind
import dev.persona.watch.pet.PetBridge
import dev.persona.watch.pet.PetPack
import dev.persona.watch.pet.PetStatus
import org.json.JSONObject
import java.net.URI

/** Reads the saved real Hub and primes its own caches; no synthetic data or remote action is submitted. */
class LiveSurfaceChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext

    fun run(): Bundle {
        check(BuildConfig.DEBUG) { "live_surfaces_requires_debug" }
        val config = checkNotNull(CredentialStore(context).load()) { "live_surfaces_connection_required" }.validated()
        val host = URI(config.baseUrl).host.lowercase().removePrefix("[").removeSuffix("]")
        check(URI(config.baseUrl).scheme == "https" && config.fingerprint != null &&
            !config.deviceId.isNullOrBlank() && config.token != "persona-wear-fixture-only" &&
            host != "localhost" && host != "::1" && !host.startsWith("127.")) { "live_surfaces_pinned_remote_connection_required" }
        check(HubDirectory(context).additional().isEmpty()) { "live_surfaces_primary_connection_only" }
        val started = System.nanoTime()
        val result = Bundle().apply { putBoolean("tls", true); putBoolean("pinned", true) }

        progress("hub_snapshot")
        val productionTransport = UrlConnectionTransport()
        // Observe the existing reads before watch filtering; never log their rows or credentials.
        val snapshotTransport = HubTransport { saved, method, path, body, headers ->
            productionTransport.request(saved, method, path, body, headers).also { response ->
                val key = when (path) {
                    "/api/v1/machines" -> "machines"
                    "/api/v1/sessions" -> "sessions"
                    "/api/v1/interactions" -> "interactions"
                    else -> null
                }
                if (method == "GET" && response.status in 200..299 && key != null) {
                    val payload = JSONObject(response.body)
                    val count = if (payload.has(key) && payload.isNull(key)) 0 else payload.getJSONArray(key).length()
                    result.putInt("raw_$key", count)
                }
            }
        }
        val snapshot = HubClient(config, "en", snapshotTransport).refresh()
        val status = PetStatus.from(snapshot, unreachable = false)
        sameConnection(config)
        PetBridge.publishDurably(context, config, status, snapshot.updatedAt)
        result.putInt("machines", snapshot.machines.size)
        result.putInt("sessions", snapshot.sessions.size)
        result.putInt("interactions", snapshot.interactions.size)
        result.putString("pet_state", status.state.wire)
        result.putInt("pet_count", status.count)
        result.putLong("pet_updated_at_ms", snapshot.updatedAt)

        progress("pet_assets")
        val pack = PetPack(context.cacheDir, config)
        runCatching {
            pack.fetch()
            val verified = checkNotNull(pack.cached(verify = true))
            check(verified.assets.size == 13)
            sameConnection(config)
            PetBridge.requestUpdate(context)
            result.putInt("pet_verified_assets", verified.assets.size)
            result.putString("pet_assets_result", "available")
        }.onFailure { failure ->
            result.putString("pet_assets_result", safeFailure(failure))
            (failure as? HubException)?.status?.let { result.putInt("pet_assets_http_status", it) }
        }

        progress("extension_catalog")
        var lastHttpStatus = 0
        val transport = HubTransport { saved, method, path, body, headers ->
            productionTransport.request(saved, method, path, body, headers).also { response ->
                lastHttpStatus = response.status
                if (method == "GET" && path == "/api/v1/extensions/-/widgets" && response.status in 200..299) {
                    runCatching { JSONObject(response.body).getJSONArray("widgets").length() }.getOrNull()
                        ?.let { result.putInt("extension_catalog_raw_widgets", it) }
                }
            }
        }
        val client = ExtensionClient(config, locale = "en", transport = transport)
        val catalog = runCatching { client.widgets() }
        result.putInt("extension_catalog_http_status", lastHttpStatus)
        catalog.fold({ widgets ->
            sameConnection(config)
            val store = ExtensionComplications.store(context)
            store.saveCatalog("", config, widgets)
            result.putString("extension_catalog_result", "available")
            result.putInt("extension_widgets", widgets.size)
            for (kind in WidgetFaceKind.entries) {
                val key = kind.name.lowercase()
                val candidates = widgets.filter { ExtensionCacheStore.familyFor(it, kind) != null }
                result.putInt("extension_${key}_candidates", candidates.size)
                val widget = candidates.firstOrNull()
                if (widget == null) {
                    result.putString("extension_${key}_result", "no_matching_widget")
                    continue
                }
                val family = checkNotNull(ExtensionCacheStore.familyFor(widget, kind))
                result.putString("extension_${key}_family", family)
                progress("extension_${key}_projection")
                lastHttpStatus = 0
                runCatching {
                    val projection = client.widget(widget, family = family)
                    sameConnection(config)
                    check(store.saveProjection("", config, widget, family, projection))
                    result.putString("extension_${key}_result", "available")
                    result.putInt("extension_${key}_items", projection.items.size)
                    result.putLong("extension_${key}_expires_at_ms", projection.expiresAtMs)
                }.onFailure { result.putString("extension_${key}_result", safeFailure(it)) }
                result.putInt("extension_${key}_http_status", lastHttpStatus)
            }
            ExtensionComplications.changed(context)
        }, { result.putString("extension_catalog_result", safeFailure(it)) })

        // This inventory may require more rights than the widget catalogue. Report a denial as such.
        progress("extension_inventory")
        runCatching {
            val inventory = productionTransport.request(config, "GET", "/api/v1/extensions", null, mapOf("Accept-Language" to "en"))
            result.putInt("extension_inventory_http_status", inventory.status)
            if (inventory.status in 200..299) {
                val rows = JSONObject(inventory.body).getJSONArray("extensions")
                var enabled = 0
                var running = 0
                var uiContributors = 0
                for (index in 0 until rows.length()) {
                    val row = rows.getJSONObject(index)
                    if (row.optBoolean("enabled")) enabled++
                    if (row.optString("status") == "running") running++
                    if ((row.optJSONObject("contributes")?.optJSONArray("ui")?.length() ?: 0) > 0) uiContributors++
                }
                result.putInt("extensions_installed", rows.length())
                result.putInt("extensions_enabled", enabled)
                result.putInt("extensions_running", running)
                result.putInt("extensions_with_ui", uiContributors)
                result.putString("extension_inventory_result", "available")
            } else result.putString("extension_inventory_result", "http_${inventory.status}")
        }.onFailure { result.putString("extension_inventory_result", safeFailure(it)) }
        sameConnection(config)
        result.putBoolean("real_surfaces_probed", true)
        result.putBoolean("system_complication_tested", false)
        result.putLong("elapsed_ms", (System.nanoTime() - started) / 1_000_000)
        result.putString("stream", "OBSERVED: real pinned-TLS Hub snapshot and surface availability; only real scoped caches were updated. Unavailable or empty surfaces are reported explicitly. No face selection, rendering, taps or remote actions are claimed.\n")
        return result
    }

    private fun sameConnection(expected: HubConfig) {
        check(CredentialStore(context).load() == expected && HubDirectory(context).additional().isEmpty()) {
            "live_surfaces_connection_changed"
        }
    }

    private fun safeFailure(failure: Throwable): String = when (failure) {
        is ExtensionException -> failure.problem.name.lowercase()
        is HubException -> failure.status?.let { "http_$it" } ?: "transport_or_asset_error"
        else -> "validation_or_storage_error"
    }

    private fun progress(stage: String) = instrumentation.sendStatus(0, Bundle().apply {
        putString("real_surfaces_stage", stage)
        putString("stream", "Real Hub surfaces: $stage\n")
    })
}
