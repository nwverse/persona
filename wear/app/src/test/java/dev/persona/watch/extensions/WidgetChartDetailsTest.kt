package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubResponse
import dev.persona.watch.data.HubTransport
import org.junit.Assert.*
import org.junit.Test

class WidgetChartDetailsTest {
    private val config = HubConfig("https://hub.example", "sample-token")
    private val widget = ExtensionWidget("sample", "chart", "Chart", "Sample", listOf("large"))
    private val chart = WidgetItem.Chart("Private values", "line", "", listOf(
        WidgetSeries("Sample", listOf(WidgetPoint("One", 7.0, null, null, null, null, null)))))
    private val cached = CachedWidget(widget, "large", ExtensionWidgetView("Chart", listOf(chart), 30_000), 1_000)

    @Test fun resumingAChartPageHidesItsValuesBeforeCheckingTheSavedIdentity() {
        val details = WidgetChartDetails()
        details.open(widget, cached, chart)
        assertEquals(chart, details.value)
        // loadCatalog clears detail content before its first render and before any credential read.
        details.clear()
        assertNull(details.value)
        val next = config.copy(token = "sample-replacement")
        val recovered = recoverWidgetSelection(true, next == config, widget.key, listOf(widget), widget.key,
            cached, emptyList(), null, null, now = 2_000)
        details.reconcile(recovered.selected, recovered.projection)
        assertNull(recovered.projection)
        assertNull(details.value)
    }

    @Test fun authenticationAndUnavailableCatalogResponsesCannotLeaveChartValuesVisible() {
        for (status in listOf(401, 403, 404)) {
            val details = WidgetChartDetails()
            details.open(widget, cached, chart)
            val api = ExtensionClient(config, transport = HubTransport { _, method, path, _, _ ->
                assertEquals("GET", method)
                assertEquals("/api/v1/extensions/-/widgets", path)
                HubResponse(status, "{}")
            })
            val failure = assertThrows(ExtensionException::class.java) { api.widgets() }
            val recovered = recoverWidgetSelection(failure.problem == ExtensionProblem.RETRYABLE, true,
                widget.key, listOf(widget), widget.key, cached, listOf(widget), widget, cached, now = 2_000)
            details.reconcile(recovered.selected, recovered.projection)
            assertNull(recovered.selected)
            assertNull(recovered.projection)
            assertNull(details.value)
        }
    }

    @Test fun catalogReplacementRemovalOrNewProjectionInvalidatesOldChartDetails() {
        val replacement = widget.copy(title = "Changed chart")
        val cases = listOf(
            null to null,
            replacement to cached.copy(widget = replacement),
            widget to cached.copy(view = cached.view.copy(items = listOf(WidgetItem.Text("New content")))),
        )
        for ((selection, projection) in cases) {
            val details = WidgetChartDetails()
            details.open(widget, cached, chart)
            details.reconcile(selection, projection)
            assertNull(details.value)
        }
        val details = WidgetChartDetails()
        details.open(widget, cached, chart)
        details.reconcile(widget, cached)
        assertEquals(chart, details.value)
    }

    @Test fun chartDetailsRequireTheCurrentProjectionAndItsExactSelectedWidget() {
        val details = WidgetChartDetails()
        details.open(widget, null, chart)
        assertNull(details.value)
        details.open(widget.copy(id = "other"), cached, chart)
        assertNull(details.value)
        details.open(widget, cached, chart.copy(title = "Not in this projection"))
        assertNull(details.value)
    }
}
