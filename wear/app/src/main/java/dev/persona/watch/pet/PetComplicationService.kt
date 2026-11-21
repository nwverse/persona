package dev.persona.watch.pet

import android.app.PendingIntent
import android.content.Intent
import android.graphics.drawable.Icon
import androidx.wear.watchface.complications.data.ComplicationData
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.LongTextComplicationData
import androidx.wear.watchface.complications.data.NoDataComplicationData
import androidx.wear.watchface.complications.data.PlainComplicationText
import androidx.wear.watchface.complications.data.ShortTextComplicationData
import androidx.wear.watchface.complications.data.SmallImage
import androidx.wear.watchface.complications.data.SmallImageComplicationData
import androidx.wear.watchface.complications.data.SmallImageType
import androidx.wear.watchface.complications.data.TimeRange
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceService
import androidx.wear.watchface.complications.datasource.ComplicationDataTimeline
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import androidx.wear.watchface.complications.datasource.TimeInterval
import androidx.wear.watchface.complications.datasource.TimelineEntry
import dev.persona.watch.CredentialStore
import dev.persona.watch.R
import dev.persona.watch.data.HubConfig
import java.time.Instant

/** Cached, static faces only. The complication never opens a network connection. */
class PetComplicationService : ComplicationDataSourceService() {
    private val timelines = PetTimelineWorker()

    override fun onComplicationRequest(request: ComplicationRequest, listener: ComplicationRequestListener) {
        timelines.submitForIdentity(currentIdentity = { CredentialStore(this).load() },
            build = { config -> timeline(request.complicationType, config) }) { timeline ->
            if (timeline == null) listener.onComplicationData(NoDataComplicationData())
            else listener.onComplicationDataTimeline(timeline)
        }
    }

    override fun getPreviewData(type: ComplicationType): ComplicationData = runCatching {
        val cached = cached() ?: return@runCatching NoDataComplicationData()
        data(type, cached, cached.stored.visible())
    }.getOrElse { NoDataComplicationData() }

    internal fun timeline(type: ComplicationType, config: HubConfig): ComplicationDataTimeline? {
        if (type !in setOf(ComplicationType.SHORT_TEXT, ComplicationType.LONG_TEXT, ComplicationType.SMALL_IMAGE)) return null
        val cached = cached(config) ?: return null
        val offline = data(type, cached, PetStatus(PetState.OFFLINE))
        val now = System.currentTimeMillis()
        val interval = cached.stored.freshInterval(now)
        val entries = if (interval == null) emptyList() else {
            val start = Instant.ofEpochMilli(interval.startMs)
            val expiry = Instant.ofEpochMilli(interval.endExclusiveMs)
            // TimeRange includes its end; TimeInterval excludes it. Also bound a flattened fresh value.
            val fresh = data(type, cached, cached.stored.status, TimeRange.between(start, expiry.minusMillis(1)))
            // AndroidX caches wire objects: reusing the default as an entry creates a parcel cycle.
            val expired = data(type, cached, PetStatus(PetState.OFFLINE))
            listOf(
                TimelineEntry(TimeInterval(start, expiry), fresh),
                TimelineEntry(TimeInterval(expiry, Instant.ofEpochMilli(Long.MAX_VALUE)), expired))
        }
        // Publish the expired state explicitly as well as bounding the fresh data.
        return ComplicationDataTimeline(offline, entries)
    }

    private fun cached(config: HubConfig? = CredentialStore(this).load()): CachedPet? {
        if (config == null) return null
        val pack = PetPack(cacheDir, config)
        val manifest = pack.cached(false) ?: return null
        return CachedPet(pack, manifest, PetBridge.read(this, config))
    }

    private fun data(type: ComplicationType, cached: CachedPet, status: PetStatus, validTimeRange: TimeRange? = null): ComplicationData {
        val bitmap = cached.manifest.faces[status.state.wire]?.let { PetPresentation.face(cached.pack.file(it)) } ?: return NoDataComplicationData()
        val image = SmallImage.Builder(Icon.createWithBitmap(bitmap), SmallImageType.ICON).build()
        val label = text(PetPresentation.title(this, status))
        val tap = PendingIntent.getActivity(this, 0, Intent(this, PetActivity::class.java), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        return when (type) {
            ComplicationType.SMALL_IMAGE -> SmallImageComplicationData.Builder(image, label)
                .setTapAction(tap).setValidTimeRange(validTimeRange).build()
            ComplicationType.SHORT_TEXT -> ShortTextComplicationData.Builder(
                text(if (status.count > 0) status.count.toString() else PetPresentation.shortTitle(this, status.state)), label)
                .setSmallImage(image).setTapAction(tap).setValidTimeRange(validTimeRange).build()
            ComplicationType.LONG_TEXT -> LongTextComplicationData.Builder(label, label)
                .setTitle(text(getString(R.string.pet_title))).setSmallImage(image).setTapAction(tap).setValidTimeRange(validTimeRange).build()
            else -> NoDataComplicationData()
        }
    }

    private fun text(value: String) = PlainComplicationText.Builder(value).build()
    override fun onDestroy() { timelines.close(); super.onDestroy() }
    private data class CachedPet(val pack: PetPack, val manifest: PetManifest, val stored: PetStoredStatus)
}
