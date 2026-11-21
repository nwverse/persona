package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubException
import dev.persona.watch.data.HubResponse
import dev.persona.watch.data.HubTransport
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class ExtensionTest {
    private val config = HubConfig("https://hub.example.com", "fixture-watch-only")
    private val catalog = """{"widgets":[{"extension":"sample","id":"summary","title":"Sample summary","extension_name":"Sample","families":["large","accessory_inline"],"opens":"overview"}]}"""
    private val answer = """{"expires_at":"2030-01-01T00:01:00Z","view":{"title":"Sample","sections":[{"items":[{"type":"metric","label":"Sample count","value":"3"}]}]}}"""

    @Test fun catalogAcceptsOnlyBoundedKnownPlacementsAndDeduplicates() {
        val list = JSONObject(catalog).getJSONArray("widgets")
        list.put(JSONObject(list.getJSONObject(0).toString()))
        list.put(JSONObject().put("extension", "../other").put("id", "summary").put("title", "Bad").put("families", JSONArray().put("large")))
        list.put(JSONObject().put("extension", "sample").put("id", "other").put("title", "Future").put("families", JSONArray().put("interactive")))
        val result = ExtensionParser.catalog(JSONObject().put("widgets", list))
        assertEquals(1, result.size)
        assertEquals("large", result.single().family)
        assertEquals("overview", result.single().opens)
        assertThrows(ExtensionException::class.java) { ExtensionParser.catalog(JSONObject()) }
    }

    @Test fun linksCannotChangeConnectionOrSelectExecutableState() {
        val widgets = ExtensionParser.catalog(JSONObject(catalog))
        val link = ExtensionLink.parse("persona://extension/sample/overview?group=", "")!!
        assertEquals("sample/summary", link.projection(widgets)!!.key)
        listOf(
            "https://extension/sample/overview?group=", "persona://user@extension/sample?group=",
            "persona://extension:443/sample?group=", "persona://extension/sample?group=other",
            "persona://extension/sample?group=&group=", "persona://extension/sample?group=&token=secret",
            "persona://extension/sample?group=&state=run", "persona://extension/sample?group=#fragment",
            "persona://extension/%2e%2e/overview?group=", "persona://extension/sample/../overview?group=",
            "persona://extension/sample?group=%", "persona://extension/sample"
        ).forEach { assertNull(it, ExtensionLink.parse(it, "")) }
        assertNull(ExtensionLink.parse("persona://extension/missing?group=")!!.projection(widgets))
    }

    @Test fun rendererModelExcludesActionsAndKeepsOnlyReadonlyValues() {
        val items = JSONArray()
            .put(JSONObject().put("type", "button").put("title", "Execute").put("action", "dangerous"))
            .put(JSONObject().put("type", "field").put("name", "secret"))
            .put(JSONObject().put("type", "link").put("url", "https://outside.example.com"))
            .put(JSONObject().put("type", "row").put("title", "Sample row").put("action", "dangerous"))
            .put(JSONObject().put("type", "text").put("text", "Sample text"))
        val view = ExtensionParser.view(view(items), "large")
        assertEquals(2, view.items.size)
        assertEquals("Sample row", (view.items[0] as WidgetItem.Row).title)
        assertEquals("Sample text", (view.items[1] as WidgetItem.Text).text)
        assertFalse(view.toString().contains("dangerous"))
        assertFalse(view.toString().contains("outside.example.com"))
    }

    @Test fun projectionLimitsMatchWatchFamilies() {
        val items = JSONArray()
        repeat(30) { items.put(JSONObject().put("type", "text").put("text", "x".repeat(250))) }
        assertEquals(12, ExtensionParser.view(view(items), "large").items.size)
        assertEquals(6, ExtensionParser.view(view(items), "medium").items.size)
        assertEquals(4, ExtensionParser.view(view(items), "small").items.size)
        val inline = ExtensionParser.view(view(items), "accessory_inline")
        assertEquals(1, inline.items.size)
        assertEquals(200, (inline.items.single() as WidgetItem.Text).text.length)
        val sections = JSONArray().put(JSONObject().put("items", JSONArray()))
            .put(JSONObject().put("items", JSONArray())).put(JSONObject().put("items", items))
        assertTrue(ExtensionParser.view(JSONObject().put("view", JSONObject().put("sections", sections)), "large").items.isEmpty())
    }

    @Test fun progressRejectsNonNumbersAndClampsFiniteRatio() {
        val items = JSONArray().put(JSONObject().put("type", "progress").put("ratio", "NaN"))
            .put(JSONObject().put("type", "progress").put("label", "Sample").put("ratio", 2))
        val progress = ExtensionParser.view(view(items), "large").items.single() as WidgetItem.Progress
        assertEquals(1.0, progress.ratio, 0.0)
        assertTrue(ExtensionParser.view(view(items), "accessory_inline").items.isEmpty())
    }

    @Test fun chartsHaveBoundedSeriesAndPointsAndPreserveCandleValues() {
        val points = JSONArray()
        repeat(80) { points.put(JSONObject().put("label", "Sample $it").put("value", it)
            .put("open", 2).put("high", 5).put("low", 1).put("close", 4)) }
        val series = JSONArray()
        repeat(6) { series.put(JSONObject().put("name", "Series $it").put("points", points)) }
        val chart = JSONObject().put("type", "chart").put("kind", "candlestick").put("series", series)
        val parsed = ExtensionParser.view(view(JSONArray().put(chart)), "large").items.single() as WidgetItem.Chart
        assertEquals(4, parsed.series.size)
        assertEquals(64, parsed.series[0].points.size)
        assertEquals(5.0, parsed.series[0].points[0].high!!, 0.0)
        chart.put("kind", "executable")
        assertTrue(ExtensionParser.view(view(JSONArray().put(chart)), "large").items.isEmpty())
    }

    @Test fun smallStatsRetainTwoAndLargeFour() {
        val values = JSONArray()
        repeat(7) { values.put(JSONObject().put("label", "Sample $it").put("value", "1")) }
        val items = JSONArray().put(JSONObject().put("type", "stat_grid").put("stats", values))
        assertEquals(2, (ExtensionParser.view(view(items), "small").items.single() as WidgetItem.Stats).values.size)
        assertEquals(4, (ExtensionParser.view(view(items), "large").items.single() as WidgetItem.Stats).values.size)
    }

    @Test fun requestsOnlyCatalogAndSelectedProjectionWithReadonlyContext() {
        val requests = mutableListOf<String>()
        val client = ExtensionClient(config, "zh-Hans", "UTC", HubTransport { c, method, path, body, headers ->
            assertEquals(config, c)
            assertEquals("zh-Hans", headers["Accept-Language"])
            requests += "$method $path"
            if (method == "GET") {
                assertNull(body)
                HubResponse(200, catalog)
            } else {
                val json = JSONObject(body!!)
                assertEquals(setOf("context"), json.keys().asSequence().toSet())
                val context = json.getJSONObject("context")
                assertEquals(setOf("size", "tz", "locale", "platform", "ui_version"), context.keys().asSequence().toSet())
                assertEquals("large", context.getString("size"))
                assertEquals("android", context.getString("platform"))
                assertEquals(3, context.getInt("ui_version"))
                HubResponse(200, answer)
            }
        }, now = { 0 })
        val widget = client.widgets().single()
        client.widget(widget)
        assertEquals(listOf("GET /api/v1/extensions/-/widgets", "POST /api/v1/extensions/sample/-/widget/summary"), requests)
    }

    @Test fun refusesForgedPlacementBeforeNetwork() {
        var calls = 0
        val client = ExtensionClient(config, transport = HubTransport { _, _, _, _, _ -> calls++; HubResponse(200, catalog) })
        val widget = client.widgets().single()
        assertEquals(ExtensionProblem.UNAVAILABLE, assertThrows(ExtensionException::class.java) { client.widget(widget.copy(owner = "other")) }.problem)
        assertEquals(ExtensionProblem.UNAVAILABLE, assertThrows(ExtensionException::class.java) { client.widget(widget.copy(families = listOf("untrusted"))) }.problem)
        assertEquals(ExtensionProblem.UNAVAILABLE, assertThrows(ExtensionException::class.java) { client.widget(widget.copy(families = emptyList())) }.problem)
        assertEquals(ExtensionProblem.UNAVAILABLE, assertThrows(ExtensionException::class.java) { client.widget(widget, family = "unsupported") }.problem)
        assertEquals(1, calls)
    }

    @Test fun freshCacheExpiresAndExplicitRefreshBypassesIt() {
        var now = 0L
        var draws = 0
        val client = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            if (method == "GET") HubResponse(200, catalog) else { draws++; HubResponse(200, answer) }
        }, now = { now })
        val widget = client.widgets().single()
        client.widget(widget); client.widget(widget)
        assertEquals(1, draws)
        client.widget(widget, force = true)
        assertEquals(2, draws)
        now = 2_000_000_000_000L
        client.widget(widget)
        assertEquals(3, draws)
    }

    @Test fun memoryProjectionExpiresAtOneHourEvenWithFarFutureProviderExpiry() {
        var clock = 1_000L
        var draws = 0
        val client = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            if (method == "GET") HubResponse(200, catalog) else { draws++; HubResponse(200, answer) }
        }, now = { clock })
        val widget = client.widgets().single()
        client.widget(widget)
        clock += CachedWidget.MAX_AGE_MS - 1
        client.widget(widget)
        assertEquals(1, draws)
        clock++
        client.widget(widget)
        assertEquals(2, draws)
        clock += CachedWidget.MAX_AGE_MS
        client.widget(widget)
        assertEquals(3, draws)
    }

    @Test fun memoryProjectionDoesNotReuseFutureDatedFetchAfterClockRollback() {
        var clock = 10_000L
        var draws = 0
        val client = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            if (method == "GET") HubResponse(200, catalog) else { draws++; HubResponse(200, answer) }
        }, now = { clock })
        val widget = client.widgets().single()
        client.widget(widget)
        clock--
        client.widget(widget)
        assertEquals(2, draws)
    }

    @Test fun cacheDoesNotReuseFullProjectionForAnAccessoryFamily() {
        val placements = mutableListOf<String>()
        val client = ExtensionClient(config, transport = HubTransport { _, method, _, body, _ ->
            if (method == "GET") HubResponse(200, catalog) else {
                placements += JSONObject(body!!).getJSONObject("context").getString("size")
                HubResponse(200, answer)
            }
        }, now = { 0 })
        val widget = client.widgets().single()
        client.widget(widget)
        client.widget(widget, family = "accessory_inline")
        client.widget(widget)
        client.widget(widget, family = "accessory_inline")
        assertEquals(listOf("large", "accessory_inline"), placements)
    }

    @Test fun authFailureRevokesCatalogAndCachedProjection() {
        var status = 200
        val client = ExtensionClient(config, transport = HubTransport { _, method, _, _, _ ->
            HubResponse(status, if (method == "GET") catalog else answer)
        }, now = { 0 })
        val widget = client.widgets().single()
        client.widget(widget)
        status = 403
        assertEquals(ExtensionProblem.FORBIDDEN, assertThrows(ExtensionException::class.java) { client.widget(widget, force = true) }.problem)
        assertEquals(ExtensionProblem.UNAVAILABLE, assertThrows(ExtensionException::class.java) { client.widget(widget) }.problem)
    }

    @Test fun unsupportedAndTransientFailuresStayDistinctWithoutLeakingBodies() {
        mapOf(401 to ExtensionProblem.AUTHENTICATION, 403 to ExtensionProblem.FORBIDDEN,
            404 to ExtensionProblem.UNAVAILABLE, 503 to ExtensionProblem.RETRYABLE, 302 to ExtensionProblem.INVALID_RESPONSE).forEach { (status, expected) ->
            val client = ExtensionClient(config, transport = HubTransport { _, _, _, _, _ -> HubResponse(status, "private token ${config.token}") })
            val error = assertThrows(ExtensionException::class.java) { client.widgets() }
            assertEquals(expected, error.problem)
            assertFalse(error.toString().contains(config.token))
        }
        val offline = ExtensionClient(config, transport = HubTransport { _, _, _, _, _ -> throw HubException("timeout") })
        assertEquals(ExtensionProblem.RETRYABLE, assertThrows(ExtensionException::class.java) { offline.widgets() }.problem)
    }

    @Test fun validatesBodiesAndSanitizesContextMetadata() {
        val client = ExtensionClient(config, "en\r\nInjected", "not/a/timezone", HubTransport { _, method, _, body, headers ->
            assertEquals("en", headers["Accept-Language"])
            if (method == "GET") HubResponse(200, catalog) else {
                assertEquals("UTC", JSONObject(body!!).getJSONObject("context").getString("tz"))
                HubResponse(200, "{".repeat(1_048_577))
            }
        })
        val widget = client.widgets().single()
        assertEquals(ExtensionProblem.INVALID_RESPONSE, assertThrows(ExtensionException::class.java) { client.widget(widget) }.problem)
    }

    private fun view(items: JSONArray) = JSONObject().put("view", JSONObject().put("sections", JSONArray().put(JSONObject().put("items", items))))
}
