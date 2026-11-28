package dev.persona.watch

import android.app.Instrumentation
import android.content.Context
import android.content.ContextWrapper
import android.os.Bundle
import android.os.Parcel
import android.os.Parcelable
import androidx.wear.watchface.complications.data.ComplicationType
import androidx.wear.watchface.complications.data.TimeRange
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceService
import androidx.wear.watchface.complications.datasource.ComplicationDataTimeline
import androidx.wear.watchface.complications.datasource.ComplicationRequest
import dev.persona.watch.extensions.ExtensionComplicationService
import dev.persona.watch.extensions.ExtensionComplications
import dev.persona.watch.extensions.WidgetFaceKind
import dev.persona.watch.pet.PetBridge
import dev.persona.watch.pet.PetComplicationService
import dev.persona.watch.pet.PetPack
import java.net.URI

/** Reads production timelines and exercises the same wire parcel path as the system callback. */
class ComplicationParcelChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext

    fun run(): Bundle {
        check(BuildConfig.DEBUG) { "parcel_requires_debug" }
        val config = checkNotNull(CredentialStore(context).load()) { "parcel_connection_required" }.validated()
        val uri = URI(config.baseUrl)
        check(config.token == "persona-wear-fixture-only" && uri.scheme == "http" &&
            uri.host in setOf("127.0.0.1", "localhost", "::1", "[::1]") && uri.userInfo == null &&
            uri.path.orEmpty() in setOf("", "/") && uri.query == null && uri.fragment == null &&
            HubDirectory(context).additional().isEmpty()) { "parcel_loopback_fixture_required" }
        check(PetPack(context.cacheDir, config).cached(verify = true)?.assets?.size == 13 &&
            PetBridge.read(context, config).freshUntil() != null) { "parcel_fresh_pet_cache_required" }

        val store = ExtensionComplications.store(context)
        val bindings = store.catalog("", config).orEmpty().flatMap { store.configured("", config, it) }
        val short = checkNotNull(bindings.firstOrNull { it.kind == WidgetFaceKind.SHORT }) { "parcel_short_binding_required" }
        val long = checkNotNull(bindings.firstOrNull { it.kind == WidgetFaceKind.LONG }) { "parcel_long_binding_required" }
        val pet = attach(PetComplicationService())
        val extension = attach(ExtensionComplicationService())
        try {
            var bytes = 0
            for (type in listOf(ComplicationType.SHORT_TEXT, ComplicationType.LONG_TEXT, ComplicationType.SMALL_IMAGE)) {
                bytes += parcel(checkNotNull(pet.timeline(type, config)), type)
            }
            for ((binding, type) in listOf(short to ComplicationType.SHORT_TEXT, long to ComplicationType.LONG_TEXT)) {
                val request = ComplicationRequest(binding.instanceId, type, false)
                bytes += parcel(checkNotNull(extension.timeline(request)), type)
            }
            check(CredentialStore(context).load() == config) { "parcel_connection_changed" }
            return Bundle().apply {
                putBoolean("complication_parcel_passed", true)
                putInt("complication_timelines", 5)
                putInt("parcel_bytes", bytes)
                putString("stream", "PASS: 3 Pet and 2 Extension production timelines have distinct default/entry objects and survive AndroidX wire Parcel round-trips; no face display or tap execution is claimed.\n")
            }
        } finally {
            pet.onDestroy()
            extension.onDestroy()
        }
    }

    private fun <T : ComplicationDataSourceService> attach(service: T): T = service.apply {
        ContextWrapper::class.java.getDeclaredMethod("attachBaseContext", Context::class.java)
            .apply { isAccessible = true }.invoke(service, context)
    }

    private fun parcel(timeline: ComplicationDataTimeline, expected: ComplicationType): Int {
        val entries = timeline.timelineEntries.toList()
        check(entries.size == 2) { "parcel_fresh_two_entry_timeline_required" }
        val values = listOf(timeline.defaultComplicationData) + entries.map { it.complicationData }
        for (i in values.indices) {
            check(values[i].type == expected && values[i].tapAction != null) { "parcel_type_or_tap_missing" }
            for (j in 0 until i) check(values[i] !== values[j]) { "parcel_default_entry_alias" }
        }
        check(values[0].validTimeRange == TimeRange.ALWAYS && values[2].validTimeRange == TimeRange.ALWAYS &&
            values[1].validTimeRange != TimeRange.ALWAYS) { "parcel_validity_mismatch" }
        check(entries[0].validity.end == entries[1].validity.start) { "parcel_timeline_gap" }
        // This is AndroidX 1.2.1's internal conversion used by onComplicationDataTimeline.
        val convert = ComplicationDataTimeline::class.java.declaredMethods.single {
            it.name.startsWith("asWireComplicationData") && it.parameterCount == 0
        }.apply { isAccessible = true }
        val wire = convert.invoke(timeline) as Parcelable
        val parcel = Parcel.obtain()
        return try {
            parcel.writeParcelable(wire, 0)
            val bytes = parcel.dataSize()
            check(bytes > 0) { "parcel_empty" }
            parcel.setDataPosition(0)
            @Suppress("DEPRECATION")
            val restored = parcel.readParcelable<Parcelable>(wire.javaClass.classLoader)
            check(restored != null && parcel.dataAvail() == 0) { "parcel_round_trip_failed" }
            bytes
        } finally { parcel.recycle() }
    }
}
