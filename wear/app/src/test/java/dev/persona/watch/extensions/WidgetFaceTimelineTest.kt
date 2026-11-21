package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class WidgetFaceTimelineTest {
    @get:Rule val temporary = TemporaryFolder()
    private val widget = ExtensionWidget("sample", "count", "Count", "Sample", listOf("accessory_circular"))
    private val view = ExtensionWidgetView("Count", listOf(WidgetItem.Text("Seven")), 120_000)
    private val cached = CachedWidget(widget, widget.family, view, 60_000)

    @Test fun clockRollbackCannotCreateAFutureFreshEntryOrRenewItsExpiry() {
        val originalExpiry = cached.freshUntilMs
        for (now in listOf(0L, 30_000L, 59_999L)) {
            assertTrue(now < cached.savedAtMs && cached.savedAtMs < originalExpiry)
            assertNull(cached.faceProjection(now))
            assertEquals(originalExpiry, cached.freshUntilMs)
        }
        val restored = checkNotNull(cached.faceProjection(cached.savedAtMs))
        assertEquals(cached.savedAtMs, restored.startMs)
        assertEquals(originalExpiry, restored.endExclusiveMs)
    }

    @Test fun aMidlifeRequestPreservesTheOriginalIntervalAndBothExpiryBounds() {
        for (projection in listOf(cached, cached.copy(view = view.copy(expiresAtMs = Long.MAX_VALUE)))) {
            val now = projection.savedAtMs + 30_000
            val interval = checkNotNull(projection.faceProjection(now))
            assertTrue(interval.startMs <= now)
            assertEquals(projection.savedAtMs, interval.startMs)
            assertEquals(minOf(projection.view.expiresAtMs, projection.savedAtMs + CachedWidget.MAX_AGE_MS), interval.endExclusiveMs)
            assertNotNull(projection.faceProjection(interval.endExclusiveMs - 1))
            assertNull(projection.faceProjection(interval.endExclusiveMs))
        }
    }

    @Test fun faceProjectionCannotBypassCredentialOrReadableContentChecks() {
        val config = HubConfig("https://hub.example", "sample-token")
        val store = ExtensionCacheStore(temporary.newFolder()) { cached.savedAtMs }
        store.saveCatalog("", config, listOf(widget))
        store.saveProjection("", config, widget, widget.family, view)
        assertNull(store.projection("", config.copy(token = "sample-replacement"), widget, widget.family)?.faceProjection(90_000))
        assertNotNull(store.projection("", config, widget, widget.family)?.faceProjection(90_000))
        assertNull(cached.copy(view = view.copy(items = emptyList())).faceProjection(90_000))
    }
}
