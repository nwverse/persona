package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubResponse
import dev.persona.watch.data.HubTransport
import org.junit.Assert.*
import org.junit.Test

class WidgetConfigCatalogTest {
    private val config = HubConfig("https://hub.example.com", "fixture-watch-only")
    private val firstCatalog = """{"widgets":[{"extension":"sample","id":"first","title":"First","families":["accessory_inline"]}]}"""
    private val secondCatalog = """{"widgets":[{"extension":"sample","id":"second","title":"Second","families":["accessory_inline"]}]}"""
    private val answer = """{"expires_at":"2030-01-01T00:00:00Z","view":{"title":"Preview","sections":[{"items":[{"type":"text","text":"Ready"}]}]}}"""

    private inner class Source(val json: String = firstCatalog, val saved: HubConfig = config) {
        var catalogStatus = 200
        var projectionStatus = 200
        val requests = mutableListOf<String>()
        val api = ExtensionClient(saved, transport = HubTransport { actual, method, path, _, _ ->
            assertEquals(saved, actual)
            requests += "$method $path"
            when {
                method == "GET" && path == "/api/v1/extensions/-/widgets" -> HubResponse(catalogStatus, json)
                method == "POST" && path in setOf("/api/v1/extensions/sample/-/widget/first", "/api/v1/extensions/sample/-/widget/second") ->
                    HubResponse(projectionStatus, answer)
                else -> throw AssertionError("Unexpected widget picker request: $method $path")
            }
        }, now = { 1_000L })
    }

    private fun WidgetConfigCatalog.load(source: Source, time: Long = 1_000L) {
        publish(WidgetConfigCatalog.Loaded.discover(source.api, WidgetFaceKind.SHORT) { time })
    }

    @Test fun transientCatalogFailureDuringBackReloadDoesNotKeepAnOldClientAndRecovers() {
        val state = WidgetConfigCatalog()
        val first = Source()
        state.load(first)
        val previous = state.widgets.single()
        assertEquals("Preview", state.clientFor(previous, 1_000)!!.widget(previous, family = "accessory_inline").title)

        // Preview Back starts loadCatalog: clear the pair before work, publish only a successful result.
        state.clear()
        val failed = Source().apply { catalogStatus = 503 }
        assertEquals(ExtensionProblem.RETRYABLE, assertThrows(ExtensionException::class.java) { state.load(failed) }.problem)
        assertTrue(state.widgets.isEmpty())
        assertNull(state.clientFor(previous, 1_000))
        assertEquals(listOf("GET /api/v1/extensions/-/widgets"), failed.requests)

        val recovered = Source(secondCatalog)
        state.load(recovered)
        val selected = state.widgets.single()
        assertNull(state.clientFor(previous, 1_000))
        assertSame(recovered.api, state.clientFor(selected, 1_000))
        assertEquals("Preview", state.clientFor(selected, 1_000)!!.widget(selected, family = "accessory_inline").title)
        assertEquals(listOf("GET /api/v1/extensions/-/widgets", "POST /api/v1/extensions/sample/-/widget/second"), recovered.requests)
        assertEquals(2, first.requests.size) // The prior client was never reused after navigation.
    }

    @Test fun revokedPreviewCannotBeRetriedWithTheClearedClientCatalog() {
        for (status in listOf(401, 403)) {
            val state = WidgetConfigCatalog()
            val first = Source()
            state.load(first)
            val selected = state.widgets.single()
            first.projectionStatus = status
            assertThrows(ExtensionException::class.java) {
                state.clientFor(selected, 1_000)!!.widget(selected, force = true, family = "accessory_inline")
            }
            assertFalse(first.api.canProject(selected))
            assertNull(state.clientFor(selected, 1_000)) // Retry must rediscover, rather than call the revoked client.

            state.clear()
            val recovered = Source()
            state.load(recovered)
            assertSame(recovered.api, state.clientFor(selected, 1_000))
            assertEquals("Preview", state.clientFor(selected, 1_000)!!.widget(selected, family = "accessory_inline").title)
            assertEquals(2, first.requests.size)
        }
    }

    @Test fun successfulPreviewBackPublishesTheNewCatalogAndItsOwnClientTogether() {
        val state = WidgetConfigCatalog()
        val original = Source()
        state.load(original)
        val first = state.widgets.single()
        state.clientFor(first, 1_000)!!.widget(first, family = "accessory_inline")
        state.clear()
        assertTrue(state.widgets.isEmpty())
        assertNull(state.clientFor(first, 1_000))
        val next = Source(secondCatalog)
        state.load(next)
        val second = state.widgets.single()
        assertEquals("second", second.id)
        assertNull(state.clientFor(first, 1_000))
        assertSame(next.api, state.clientFor(second, 1_000))
        state.clientFor(second, 1_000)!!.widget(second, family = "accessory_inline")
        assertEquals("POST /api/v1/extensions/sample/-/widget/second", next.requests.last())
    }

    @Test fun returningThroughTheConnectionListCannotReuseACollidingWidgetFromAnotherHub() {
        val state = WidgetConfigCatalog()
        val primary = Source()
        state.load(primary)
        val sameWidget = state.widgets.single()
        // The multi-connection Back path calls loadGroups, which clears this same state.
        state.clear()
        assertNull(state.clientFor(sameWidget, 1_000))
        val additional = Source(saved = HubConfig("https://second.example.com", "fixture-other-watch-only"))
        state.load(additional)
        assertEquals(sameWidget, state.widgets.single())
        assertSame(additional.api, state.clientFor(sameWidget, 1_000))
        state.clientFor(sameWidget, 1_000)!!.widget(sameWidget, family = "accessory_inline")
        assertEquals(listOf("GET /api/v1/extensions/-/widgets"), primary.requests)
        assertEquals(2, additional.requests.size)
    }

    @Test fun expiredOrFutureDatedCatalogRequiresDiscoveryBeforePreview() {
        val state = WidgetConfigCatalog()
        val source = Source()
        state.load(source)
        val selected = state.widgets.single()
        assertSame(source.api, state.clientFor(selected, 1_000))
        assertSame(source.api, state.clientFor(selected, 1_000 + CachedWidget.MAX_AGE_MS - 1))
        assertNull(state.clientFor(selected, 1_000 + CachedWidget.MAX_AGE_MS))
        assertNull(state.clientFor(selected, Long.MAX_VALUE))
        assertNull(state.clientFor(selected, 999))
        assertEquals(listOf("GET /api/v1/extensions/-/widgets"), source.requests)
    }

    @Test fun onlyWidgetsDiscoveredForThisComplicationKindCanBeSelected() {
        val state = WidgetConfigCatalog()
        val unsupported = Source("""{"widgets":[{"extension":"sample","id":"first","title":"Large only","families":["large"]}]}""")
        val loaded = WidgetConfigCatalog.Loaded.discover(unsupported.api, WidgetFaceKind.SHORT) { 1_000L }
        state.publish(loaded)
        assertEquals(1, loaded.allWidgets.size)
        assertTrue(state.widgets.isEmpty())
        assertNull(state.clientFor(loaded.allWidgets.single(), 1_000))
    }
}
