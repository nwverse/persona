package dev.persona.watch.pet

import android.content.ComponentName
import android.content.Context
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceUpdateRequester
import dev.persona.watch.data.HubConfig
import dev.persona.watch.HubGroupRead
import dev.persona.watch.data.WatchSnapshot
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/** MainActivity can publish its existing poll results; the pet adds no session polling in the background. */
object PetBridge {
    private val worker = Executors.newSingleThreadExecutor()
    private val fetching = AtomicBoolean(false)

    fun hasCached(context: Context, config: HubConfig): Boolean = PetPack(context.cacheDir, config).cached(false) != null

    fun refresh(context: Context, config: HubConfig, snapshot: WatchSnapshot?, unreachable: Boolean = false, partlyUnreachable: Boolean = false) {
        val app = context.applicationContext
        val next = PetFreshness.next(read(app, config), snapshot, unreachable, partlyUnreachable)
        publish(app, config, next.status, next.updatedAt)
        refreshAssets(app, config)
    }

    fun refreshGroups(context: Context, config: HubConfig, result: HubGroupRead, groupCount: Int) {
        val app = context.applicationContext
        val next = PetFreshness.groups(read(app, config), result, groupCount)
        publish(app, config, next.status, next.updatedAt)
        refreshAssets(app, config)
    }

    private fun refreshAssets(app: Context, config: HubConfig) {
        if (!fetching.compareAndSet(false, true)) return
        worker.execute {
            try {
                val preferences = preferences(app, config)
                val now = System.currentTimeMillis()
                if (now - preferences.getLong("assets_attempt", 0) > ASSET_RETRY_MS) {
                    preferences.edit().putLong("assets_attempt", now).apply()
                    runCatching { PetPack(app.cacheDir, config).fetch() }.onSuccess { requestUpdate(app) }
                }
            } finally { fetching.set(false) }
        }
    }

    fun publish(context: Context, config: HubConfig, status: PetStatus, updatedAt: Long) {
        val preferences = preferences(context, config)
        val previous = read(context, config)
        val previousRequest = preferences.getLong("widget_requested_at", 0)
        val now = System.currentTimeMillis()
        val visible = PetStoredStatus(status, updatedAt).visible(now)
        val next = PetStoredStatus(visible, updatedAt)
        preferences.edit().putString("state", visible.state.wire).putInt("count", visible.count)
            .putLong("updated_at", updatedAt).apply()
        if (petPublicationNeedsUpdate(previous, next, previousRequest, now)) {
            preferences.edit().putLong("widget_requested_at", now).apply()
            requestUpdate(context)
        }
    }

    fun read(context: Context, config: HubConfig): PetStoredStatus {
        val preferences = preferences(context, config)
        val state = PetState.entries.firstOrNull { it.wire == preferences.getString("state", "") } ?: PetState.OFFLINE
        return PetStoredStatus(PetStatus(state, preferences.getInt("count", 0).coerceAtLeast(0)), preferences.getLong("updated_at", 0))
    }

    /** Test/probe callers need durable state before exiting or removing their restore backup. */
    fun publishDurably(context: Context, config: HubConfig, status: PetStatus, updatedAt: Long): PetStoredStatus {
        val now = System.currentTimeMillis()
        val stored = PetStoredStatus(PetStoredStatus(status, updatedAt).visible(now), updatedAt)
        return commitPetPublication(stored, commit = { next ->
            preferences(context, config).edit().putString("state", next.status.state.wire)
                .putInt("count", next.status.count).putLong("updated_at", next.updatedAt)
                .putLong("widget_requested_at", now).commit()
        }, requestUpdate = { requestUpdate(context) })
    }

    fun requestUpdate(context: Context) {
        runCatching { ComplicationDataSourceUpdateRequester.create(context,
            ComponentName(context, PetComplicationService::class.java)).requestUpdateAll() }
    }

    private fun preferences(context: Context, config: HubConfig) = context.getSharedPreferences(petStatusPreferencesName(config), Context.MODE_PRIVATE)
    private const val ASSET_RETRY_MS = 60 * 60 * 1000L
}
