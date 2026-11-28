package dev.persona.watch.extensions

import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import androidx.wear.watchface.complications.data.ComplicationData
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.LongTextComplicationData
import androidx.wear.watchface.complications.data.NoDataComplicationData
import androidx.wear.watchface.complications.data.PlainComplicationText
import androidx.wear.watchface.complications.data.ShortTextComplicationData
import androidx.wear.watchface.complications.data.TimeRange
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceService
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceUpdateRequester
import androidx.wear.watchface.complications.datasource.ComplicationDataTimeline
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import androidx.wear.watchface.complications.datasource.TimeInterval
import androidx.wear.watchface.complications.datasource.TimelineEntry
import dev.persona.watch.CredentialStore
import dev.persona.watch.HubDirectory
import dev.persona.watch.R
import dev.persona.watch.data.HubConfig
import java.io.File
import java.time.Instant
import java.util.concurrent.Executors

/** The system entry only reads private snapshots. It never contacts an extension or a hub. */
class ExtensionComplicationService : ComplicationDataSourceService() {
    private val disk = Executors.newSingleThreadExecutor()
    override fun onComplicationRequest(request: ComplicationRequest, listener: ComplicationRequestListener) {
        disk.execute {
            val timeline = runCatching { timeline(request) }.getOrNull()
            if (timeline == null) listener.onComplicationData(NoDataComplicationData())
            else listener.onComplicationDataTimeline(timeline)
        }
    }

    override fun getPreviewData(type: ComplicationType): ComplicationData? = when (type) {
        ComplicationType.SHORT_TEXT -> ShortTextComplicationData.Builder(text("42"), text(getString(R.string.extension_face_preview)))
            .setTitle(text(getString(R.string.extension_face_sample))).build()
        ComplicationType.LONG_TEXT -> LongTextComplicationData.Builder(text(getString(R.string.extension_face_preview)), text(getString(R.string.extension_face_preview)))
            .setTitle(text(getString(R.string.extension_widgets))).build()
        else -> null
    }

    internal fun timeline(request: ComplicationRequest): ComplicationDataTimeline? {
        val kind = when (request.complicationType) { ComplicationType.SHORT_TEXT -> WidgetFaceKind.SHORT; ComplicationType.LONG_TEXT -> WidgetFaceKind.LONG; else -> return null }
        val store = ExtensionComplications.store(this)
        val binding = store.binding(request.complicationInstanceId)?.takeIf { it.kind == kind } ?: return null
        val config = ExtensionComplications.config(this, binding.group) ?: return null
        if (binding.identity != ExtensionCacheStore.identity(config)) return null
        val widget = store.catalog(binding.group, config)?.firstOrNull {
            it.owner == binding.owner && it.id == binding.widget && binding.family in it.families && binding.family in ExtensionCacheStore.families(kind)
        } ?: return null
        val cached = store.projection(binding.group, config, widget, binding.family)
        val tap = PendingIntent.getActivity(this, request.complicationInstanceId,
            Intent(this, ExtensionWidgetsActivity::class.java).setAction("dev.persona.watch.extension.OPEN_${request.complicationInstanceId}")
                .putExtra("group", binding.group).putExtra("widget_key", widget.key),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val stale = data(kind, FaceWords(widget.title, getString(R.string.extension_face_refresh), getString(R.string.extension_face_refresh_detail)), tap)
        val now = System.currentTimeMillis()
        val projection = cached?.faceProjection(now)
        val entries = if (projection == null) emptyList() else {
            val start = Instant.ofEpochMilli(projection.startMs)
            val expiry = Instant.ofEpochMilli(projection.endExclusiveMs)
            // TimeRange's inclusive end must precede the exclusive timeline boundary.
            val fresh = data(kind, projection.words, tap, TimeRange.between(start, expiry.minusMillis(1)))
            // AndroidX caches wire objects: reusing the default as an entry creates a parcel cycle.
            val expired = data(kind, FaceWords(widget.title, getString(R.string.extension_face_refresh), getString(R.string.extension_face_refresh_detail)), tap)
            listOf(
                TimelineEntry(TimeInterval(start, expiry), fresh),
                TimelineEntry(TimeInterval(expiry, Instant.ofEpochMilli(Long.MAX_VALUE)), expired))
        }
        // Publish the expired state explicitly as well as bounding the fresh data.
        return ComplicationDataTimeline(stale, entries)
    }

    private fun data(kind: WidgetFaceKind, words: FaceWords, tap: PendingIntent, validTimeRange: TimeRange? = null): ComplicationData = when (kind) {
        WidgetFaceKind.SHORT -> ShortTextComplicationData.Builder(text(WidgetFaceText.clip(words.text, 6)), text(words.description))
            .setTitle(text(WidgetFaceText.clip(words.title, 6))).setTapAction(tap).setValidTimeRange(validTimeRange).build()
        WidgetFaceKind.LONG -> LongTextComplicationData.Builder(text(WidgetFaceText.clip(words.text, 160)), text(words.description))
            .setTitle(text(WidgetFaceText.clip(words.title, 40))).setTapAction(tap).setValidTimeRange(validTimeRange).build()
    }
    private fun text(value: String) = PlainComplicationText.Builder(value).build()
    override fun onComplicationDeactivated(complicationInstanceId: Int) {
        disk.execute { runCatching { ExtensionComplications.store(this).remove(complicationInstanceId) } }
    }
    override fun onDestroy() { disk.shutdown(); super.onDestroy() }
}

object ExtensionComplications {
    fun store(context: Context) = ExtensionCacheStore(File(context.noBackupFilesDir, "extension-widgets"))
    /** Envelopes and deletion fences are read from disk, not process-local directory caches. */
    fun config(context: Context, group: String): HubConfig? = when {
        group.isEmpty() -> CredentialStore(context).load()
        group.matches(Regex("[a-f0-9-]{36}")) -> HubDirectory(context).activeCredential(group)
        else -> null
    }
    fun changed(context: Context) { runCatching { ComplicationDataSourceUpdateRequester.create(context,
        ComponentName(context, ExtensionComplicationService::class.java)).requestUpdateAll() } }
}

fun ExtensionWidget.familyFor(kind: WidgetFaceKind): String? = ExtensionCacheStore.familyFor(this, kind)
