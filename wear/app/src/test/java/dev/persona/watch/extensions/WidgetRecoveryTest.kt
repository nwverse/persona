package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubException
import dev.persona.watch.data.HubResponse
import dev.persona.watch.data.HubTransport
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.Rule
import org.junit.rules.TemporaryFolder

class WidgetRecoveryTest {
    @get:Rule val temporary = TemporaryFolder()
    private val config = HubConfig("https://hub.example.com", "fixture-watch-only")
    private val catalog = """{"widgets":[{"extension":"sample","id":"summary","title":"Sample summary","families":["large"]}]}"""
    private val widget = ExtensionParser.catalog(JSONObject(catalog)).single()
    private val answer = """{"expires_at":"2030-01-01T00:00:00Z","view":{"title":"Sample","sections":[]}}"""
    private val view = ExtensionParser.view(JSONObject(answer), "large")
    private val cached = CachedWidget(widget, widget.family, view, 1_000)

    @Test fun resumeAfterPauseKeepsAnUncachedKeyOnlySelectionOnRetryableFailure() {
        val previousWidgets = listOf(widget)
        // The foreground read hides widgets/projection until it checks the same credential again.
        val recovered = recoverWidgetSelection(true, true, widget.key, previousWidgets,
            widget.key, null, emptyList(), null, null)
        assertEquals(previousWidgets, recovered.widgets)
        assertEquals(widget, recovered.selected)
        assertEquals(widget.key, recovered.selectedKey)
        assertNull(recovered.projection)
    }

    @Test fun retryableCatalogFailureCanFetchThePreviouslyAuthorizedUncachedProjection() {
        var catalogs = 0
        var projections = 0
        val api = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            if (method == "GET") {
                if (++catalogs > 1) throw HubException("network_failed")
                HubResponse(200, catalog)
            } else { projections++; HubResponse(200, answer) }
        }, now = { 1_000 })
        val widgets = api.widgets()
        assertEquals(ExtensionProblem.RETRYABLE, assertThrows(ExtensionException::class.java) { api.widgets() }.problem)
        val recovered = recoverWidgetSelection(true, true, widget.key, widgets, widget.key, null, emptyList(), null, null)
        assertTrue(api.canProject(recovered.selected!!))
        assertEquals(view, api.widget(recovered.selected!!))
        assertEquals(1, projections)
    }

    @Test fun diskOnlyCatalogOffersCachedRetryDetailWithoutAuthorizingAProjection() {
        var projections = 0
        val api = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            if (method != "GET") projections++
            HubResponse(503, "{}")
        })
        assertThrows(ExtensionException::class.java) { api.widgets() }
        val recovered = recoverWidgetSelection(true, false, widget.key, emptyList(), null, null,
            listOf(widget), widget, cached)
        assertEquals(widget, recovered.selected)
        assertEquals(cached, recovered.projection)
        assertFalse(api.canProject(widget))
        assertThrows(ExtensionException::class.java) { api.widget(widget) }
        assertEquals(0, projections)
    }

    @Test fun connectionChangeCannotRestorePreviousPrivateSelectionOrProjection() {
        val recovered = recoverWidgetSelection(true, false, widget.key, listOf(widget), widget.key, cached,
            emptyList(), null, null)
        assertTrue(recovered.widgets.isEmpty())
        assertNull(recovered.selected)
        assertNull(recovered.projection)
    }

    @Test fun projectionMustBelongToTheSelectedWidget() {
        val other = widget.copy(id = "other")
        val recovered = recoverWidgetSelection(true, true, other.key, listOf(widget, other), widget.key, cached,
            emptyList(), null, null)
        assertEquals(other, recovered.selected)
        assertNull(recovered.projection)
    }

    @Test fun nonRetryableFailureDiscardsSelectionAndAllCachedContent() {
        val recovered = recoverWidgetSelection(false, true, widget.key, listOf(widget), widget.key, cached,
            listOf(widget), widget, cached)
        assertEquals(RecoveredWidgetSelection(emptyList(), null, null, null), recovered)
    }

    @Test fun providerDeadlineBeyondOneDayUsesItsOriginalRemainderAtEveryWake() {
        val start = 1_000L
        val hour = 3_600_000L
        val deadline = start + 49 * hour
        assertEquals(24 * hour, WidgetExpiry.nextDelay(deadline, start))
        assertEquals(24 * hour, WidgetExpiry.nextDelay(deadline, start + 24 * hour))
        assertEquals(hour, WidgetExpiry.nextDelay(deadline, start + 48 * hour))
        assertEquals(1L, WidgetExpiry.nextDelay(deadline, deadline - 1))
        assertNull(WidgetExpiry.nextDelay(deadline, deadline))
        assertNull(WidgetExpiry.nextDelay(deadline, deadline + hour))
    }

    @Test fun delayedWakeAndClockChangesNeverRenewTheProviderDeadline() {
        val day = WidgetExpiry.MAX_WAIT_MS
        val deadline = day + 5_000
        assertEquals(2_000L, WidgetExpiry.nextDelay(deadline, day + 3_000))
        assertNull(WidgetExpiry.nextDelay(deadline, deadline + 1))
        assertEquals(day, WidgetExpiry.nextDelay(deadline, -1))
        assertEquals(day, WidgetExpiry.nextDelay(Long.MAX_VALUE, Long.MIN_VALUE))
    }

    @Test fun expiredWakeRequestsFaceUpdateBeforeRenderingForProviderAndCacheDeadlines() {
        val providerBound = cached.copy(view = view.copy(expiresAtMs = cached.savedAtMs + 5_000))
        val cacheBound = cached.copy(view = view.copy(expiresAtMs = Long.MAX_VALUE))
        for (projection in listOf(providerBound, cacheBound)) {
            for (now in listOf(projection.freshUntilMs, projection.freshUntilMs + 5_000)) {
                val events = mutableListOf<String>()
                WidgetExpiry.wake(projection.freshUntilMs, now,
                    schedule = { fail("Expired content must not schedule another fresh interval") },
                    publish = { assertFalse(projection.fresh(now)); events += "publish" },
                    render = { events += "render" })
                assertEquals(listOf("publish", "render"), events)
            }
        }
    }

    @Test fun earlyWakeOnlyReschedulesUntilTheOriginalDeadlineThenPublishes() {
        val events = mutableListOf<String>()
        val start = 1_000L
        val deadline = start + WidgetExpiry.MAX_WAIT_MS + 5_000
        fun wake(now: Long) = WidgetExpiry.wake(deadline, now,
            schedule = { events += "wait:$it" }, publish = { events += "publish" }, render = { events += "render" })
        wake(start)
        wake(start + WidgetExpiry.MAX_WAIT_MS)
        wake(deadline - 1)
        assertEquals(listOf("wait:${WidgetExpiry.MAX_WAIT_MS}", "wait:5000", "wait:1"), events)
        wake(deadline)
        assertEquals(listOf("wait:${WidgetExpiry.MAX_WAIT_MS}", "wait:5000", "wait:1", "publish", "render"), events)
    }

    @Test fun freshProjectionSurvivesCatalogAndProjectionRetryFailuresWithoutRenewingItsAge() {
        var offline = false
        var time = 1_000L
        val api = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            if (offline) HubResponse(503, "{}") else HubResponse(200, if (method == "GET") catalog else answer)
        }, now = { time })
        val widgets = api.widgets()
        val first = api.projection(widget)
        offline = true; time += 10_000
        // The same production recovery helper publishes the local preview before either Hub request.
        val preview = recoverWidgetSelection(true, true, widget.key, widgets, widget.key, first,
            emptyList(), null, null, time)
        assertSame(first, preview.projection)
        assertTrue(preview.projection!!.fresh(time))
        assertEquals(ExtensionProblem.RETRYABLE, assertThrows(ExtensionException::class.java) { api.widgets() }.problem)
        val restored = recoverWidgetSelection(true, true, widget.key, widgets, widget.key, first,
            emptyList(), null, null, time)
        assertEquals(ExtensionProblem.RETRYABLE, assertThrows(ExtensionException::class.java) { api.projection(widget, force = true) }.problem)
        assertSame(first, retainWidgetProjection(widget, true, restored.projection, null, time))
        assertTrue(first.fresh(time)) // A retryable problem alone is not a stale projection.
        assertFalse(first.fresh(first.freshUntilMs))
    }

    @Test fun identityCheckedDiskViewRemainsVisibleDuringFirstProjectionRetry() {
        val api = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            if (method == "GET") HubResponse(200, catalog) else HubResponse(503, "{}")
        }, now = { 2_000 })
        val preview = recoverWidgetSelection(true, false, widget.key, emptyList(), null, null,
            listOf(widget), widget, cached, 2_000)
        assertSame(cached, preview.projection)
        api.widgets()
        assertThrows(ExtensionException::class.java) { api.projection(widget, force = true) }
        assertSame(cached, retainWidgetProjection(widget, true, preview.projection, null, 2_000))
        assertTrue(cached.fresh(2_000))
    }

    @Test fun previewUsesTheFreshMatchingSourceAndOnlyItsOwnDeadline() {
        val now = CachedWidget.MAX_AGE_MS + 1_000
        val fresh = cached.copy(savedAtMs = now, view = view.copy(expiresAtMs = now + 5_000))
        assertSame(fresh, retainWidgetProjection(widget, true, cached, fresh, now))
        assertTrue(fresh.fresh(now + 4_999))
        assertFalse(fresh.fresh(now + 5_000))
        assertNull(retainWidgetProjection(widget.copy(id = "other"), true, fresh, null, now))
        assertNull(retainWidgetProjection(widget, false, fresh, null, now))
        assertNull(retainWidgetProjection(widget, true, fresh.copy(family = "accessoryInline"), null, now))
        // Expired content keeps its original time and must be labelled stale, including clock rollback.
        assertSame(cached, retainWidgetProjection(widget, true, cached, null, now))
        assertFalse(cached.fresh(now))
        assertFalse(cached.fresh(cached.savedAtMs - 1))
    }

    @Test fun reusedClientProjectionDoesNotRefreshItsDiskOrPageTimestamp() {
        var time = 1_000L
        val api = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            HubResponse(200, if (method == "GET") catalog else answer)
        }, now = { time })
        val disk = ExtensionCacheStore(temporary.newFolder(), now = { time })
        disk.saveCatalog("", config, api.widgets())
        val first = api.projection(widget)
        time = first.savedAtMs + CachedWidget.MAX_AGE_MS - 1
        val reused = api.projection(widget)
        assertSame(first, reused)
        assertTrue(disk.saveProjection("", config, widget, widget.family, reused.view, reused.savedAtMs))
        val saved = disk.projection("", config, widget, widget.family)!!
        assertEquals(first.savedAtMs, saved.savedAtMs)
        assertTrue(saved.fresh(time))
        assertFalse(saved.fresh(time + 1))
    }
}
