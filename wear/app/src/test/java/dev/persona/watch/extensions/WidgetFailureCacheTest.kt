package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubResponse
import dev.persona.watch.data.HubTransport
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class WidgetFailureCacheTest {
    @get:Rule val temporary = TemporaryFolder()
    private val config = HubConfig("https://hub.example", "sample-token")
    private val group = "11111111-1111-1111-1111-111111111111"
    private val catalog = """{"widgets":[{"extension":"sample","id":"first","title":"First","families":["large","accessory_circular"]},{"extension":"sample","id":"second","title":"Second","families":["large"]}]}"""
    private val answer = """{"expires_at":"2030-01-01T00:00:00Z","view":{"title":"Sample","sections":[{"items":[{"type":"text","text":"Ready"}]}]}}"""

    @Test fun missingProjectionEvictsOnlyItsFamilyFromDiskAndMemoryAndCanFetchAgain() {
        for (status in listOf(404, 410)) {
            var missing = false
            var projections = 0
            val api = ExtensionClient(config, transport = HubTransport { _, method, path, _, _ ->
                when (method) {
                    "GET" -> HubResponse(200, catalog)
                    "POST" -> { projections++; HubResponse(if (missing && path.endsWith("/first")) status else 200, answer) }
                    else -> throw AssertionError("Unexpected method")
                }
            }, now = { 60_000 })
            val widgets = api.widgets()
            val first = widgets[0]; val second = widgets[1]
            val disk = ExtensionCacheStore(temporary.newFolder()) { 60_000 }
            disk.saveCatalog("", config, widgets)
            disk.saveCatalog(group, config, widgets)
            for ((widget, family) in listOf(first to "large", first to "accessory_circular", second to "large")) {
                val view = api.widget(widget, family = family)
                disk.saveProjection("", config, widget, family, view)
                disk.saveProjection(group, config, widget, family, view)
            }
            disk.bind(1, "", config, first, "accessory_circular", WidgetFaceKind.SHORT)
            missing = true
            val failure = assertThrows(ExtensionException::class.java) { api.widget(first, force = true, family = "large") }
            assertEquals(ExtensionProblem.UNAVAILABLE, failure.problem)
            WidgetFailureCache.invalidate(disk, "", config, failure.problem, first, "large")
            assertNull(disk.projection("", config, first, "large"))
            assertNotNull(disk.projection("", config, first, "accessory_circular"))
            assertNotNull(disk.projection("", config, second, "large"))
            assertNotNull(disk.projection(group, config, first, "large"))
            assertEquals(widgets, disk.catalog("", config))
            assertNotNull(disk.binding(1))
            val reads = projections
            api.widget(first, family = "accessory_circular")
            api.widget(second)
            assertEquals(reads, projections)
            missing = false
            api.widget(first, family = "large")
            assertEquals(reads + 1, projections) // A vanished projection cannot return the old memory entry.
        }
    }

    @Test fun catalogUnavailabilityAndAuthenticationStillRevokeTheMatchingHubOnly() {
        for ((status, projectionFailure) in listOf(404 to false, 410 to false, 401 to true, 403 to true)) {
            var fail = false
            val api = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
                HubResponse(if (fail) status else 200, if (method == "GET") catalog else answer)
            })
            val widgets = api.widgets()
            val widget = widgets.first()
            val disk = ExtensionCacheStore(temporary.newFolder())
            disk.saveCatalog("", config, widgets); disk.saveCatalog(group, config, widgets)
            val view = api.widget(widget)
            disk.saveProjection("", config, widget, widget.family, view)
            disk.saveProjection(group, config, widget, widget.family, view)
            fail = true
            val failure = assertThrows(ExtensionException::class.java) {
                if (projectionFailure) api.widget(widget, force = true) else api.widgets()
            }
            WidgetFailureCache.invalidate(disk, "", config, failure.problem,
                widget.takeIf { projectionFailure }, widget.family.takeIf { projectionFailure })
            assertNull(disk.catalog("", config))
            assertNull(disk.projection("", config, widget, widget.family))
            assertEquals(widgets, disk.catalog(group, config))
            assertNotNull(disk.projection(group, config, widget, widget.family))
        }
    }

    @Test fun retryableFailureAndOldCredentialCannotInvalidateTheCurrentCache() {
        val api = ExtensionClient(config, transport = HubTransport { _, _, _, _, _ -> HubResponse(200, catalog) })
        val widgets = api.widgets()
        val disk = ExtensionCacheStore(temporary.newFolder())
        disk.saveCatalog("", config, widgets)
        WidgetFailureCache.invalidate(disk, "", config, ExtensionProblem.RETRYABLE, widgets.first(), "large")
        WidgetFailureCache.invalidate(disk, "", config.copy(token = "sample-old"), ExtensionProblem.AUTHENTICATION)
        assertEquals(widgets, disk.catalog("", config))
    }
}
