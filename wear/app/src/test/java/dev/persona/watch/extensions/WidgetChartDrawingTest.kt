package dev.persona.watch.extensions

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class WidgetChartDrawingTest {
    @Test fun everyAcceptedKindBuildsItsDedicatedDrawingRatherThanFallingBackToBars() {
        val expected = mapOf(
            "line" to WidgetChartDrawing.Line::class.java,
            "area" to WidgetChartDrawing.Line::class.java,
            "bar" to WidgetChartDrawing.Bars::class.java,
            "pie" to WidgetChartDrawing.Circular::class.java,
            "donut" to WidgetChartDrawing.Circular::class.java,
            "candlestick" to WidgetChartDrawing.Values::class.java,
        )
        expected.forEach { (kind, type) ->
            val chart = parsed(kind, listOf(1.0, 3.0))!!
            val drawing = WidgetChartDrawing.from(chart.kind, chart.series.single().points)
            assertEquals(kind, type, drawing.javaClass)
            when (drawing) {
                is WidgetChartDrawing.Line -> {
                    assertEquals(kind == "area", drawing.filled)
                    assertEquals(listOf(WidgetChartDrawing.Position(0.25f, 1f), WidgetChartDrawing.Position(0.75f, 0f)), drawing.points)
                    assertEquals(1f, drawing.baseline, 0f)
                }
                is WidgetChartDrawing.Bars -> {
                    assertEquals(2, drawing.points.size)
                    assertEquals(1f, drawing.baseline, 0f)
                    assertEquals(2f / 3f, drawing.points[0].y, 0.00001f)
                }
                is WidgetChartDrawing.Circular -> {
                    assertEquals(kind == "donut", drawing.donut)
                    assertEquals(listOf(WidgetChartDrawing.Slice(0, -90f, 90f), WidgetChartDrawing.Slice(1, 0f, 270f)), drawing.slices)
                }
                is WidgetChartDrawing.Values -> assertEquals(chart.series.single().points, drawing.points)
            }
        }
        assertNull(parsed("future_chart", listOf(1.0)))
        assertThrows(IllegalArgumentException::class.java) { WidgetChartDrawing.from("future_chart", points(1.0)) }
    }

    @Test fun zeroAndNegativeSlicesDoNotOccupyAnAngleOrChangeTheRemainingPointIdentity() {
        listOf("pie", "donut").forEach { kind ->
            val drawing = circular(kind, 0.0, 1.0, -2.0, 3.0, 0.0)
            assertEquals(listOf(WidgetChartDrawing.Slice(1, -90f, 90f), WidgetChartDrawing.Slice(3, 0f, 270f)), drawing.slices)
        }
    }

    @Test fun zeroTotalAndEmptySeriesDrawNoFabricatedSlice() {
        listOf("pie", "donut").forEach { kind ->
            assertTrue(circular(kind, 0.0, 0.0).slices.isEmpty())
            assertTrue(circular(kind, -1.0, 0.0).slices.isEmpty())
            assertTrue(circular(kind).slices.isEmpty())
        }
    }

    @Test fun aSinglePositiveSliceFillsTheCircleOrRing() {
        listOf("pie", "donut").forEach { kind ->
            assertEquals(listOf(WidgetChartDrawing.Slice(0, -90f, 360f)), circular(kind, 5.0).slices)
            assertEquals(listOf(WidgetChartDrawing.Slice(1, -90f, 360f)), circular(kind, 0.0, 5.0).slices)
        }
    }

    @Test fun donutStrokeLeavesAHoleWhilePieFillsToTheCenter() {
        val pie = circular("pie", 1.0).geometry(40f, 0.55f)
        assertTrue(pie.useCenter)
        assertEquals(40f, pie.radius, 0f)
        assertEquals(0f, pie.strokeWidth, 0f)
        val donut = circular("donut", 1.0).geometry(40f, 0.55f)
        assertFalse(donut.useCenter)
        assertEquals(40f, donut.radius + donut.strokeWidth / 2, 0.0001f)
        assertEquals(22f, donut.radius - donut.strokeWidth / 2, 0.0001f)
    }

    @Test fun finiteValuesWhoseRawTotalOverflowsStillHaveFiniteProportionalSlices() {
        listOf("pie", "donut").forEach { kind ->
            val drawing = circular(kind, Double.MAX_VALUE, Double.MAX_VALUE, Double.MAX_VALUE / 2)
            val expected = listOf(144f, 144f, 72f)
            assertEquals(expected.size, drawing.slices.size)
            expected.forEachIndexed { index, sweep ->
                assertEquals("$kind slice $index", sweep, drawing.slices[index].sweep, 0.0001f)
            }
            assertEquals(360f, drawing.slices.sumOf { it.sweep.toDouble() }.toFloat(), 0.0001f)
            assertTrue(drawing.slices.all { it.start.isFinite() && it.sweep.isFinite() && it.sweep > 0 })
        }
    }

    @Test fun fractionalSlicesEndAtExactlyOneTurnWithoutLeavingARoundingGap() {
        val drawing = circular("pie", 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0)
        val last = drawing.slices.last()
        assertEquals(270f, last.start + last.sweep, 0.0001f)
        assertEquals(360f, drawing.slices.sumOf { it.sweep.toDouble() }.toFloat(), 0.0001f)
    }

    @Test fun barBaselineSeparatesPositiveAndNegativeValues() {
        val drawing = WidgetChartDrawing.from("bar", points(-2.0, 0.0, 2.0)) as WidgetChartDrawing.Bars
        assertEquals(0.5f, drawing.baseline, 0f)
        assertEquals(listOf(1f, 0.5f, 0f), drawing.points.map { it.y })
    }

    @Test fun singleAndEmptyCartesianSeriesHaveFiniteGeometry() {
        listOf("line", "area", "bar").forEach { kind ->
            val drawing = WidgetChartDrawing.from(kind, points(0.0))
            val positions = when (drawing) {
                is WidgetChartDrawing.Line -> drawing.points
                is WidgetChartDrawing.Bars -> drawing.points
                else -> error("Expected Cartesian chart")
            }
            assertEquals(listOf(WidgetChartDrawing.Position(0.5f, 0.5f)), positions)
            val empty = WidgetChartDrawing.from(kind, emptyList())
            assertTrue(when (empty) { is WidgetChartDrawing.Line -> empty.points.isEmpty(); is WidgetChartDrawing.Bars -> empty.points.isEmpty(); else -> false })
        }
    }

    @Test fun candlesKeepTheExistingBoundedAccessibleOhlcPresentation() {
        val chart = parsed("candlestick", List(8) { it.toDouble() })!!
        val drawing = WidgetChartDrawing.from(chart.kind, chart.series.single().points) as WidgetChartDrawing.Values
        assertEquals(4, drawing.points.size)
        assertTrue(drawing.points.all { it.open == 1.0 && it.high == 4.0 && it.low == 0.0 && it.close == 3.0 })
    }

    private fun circular(kind: String, vararg values: Double) = WidgetChartDrawing.from(kind, points(*values)) as WidgetChartDrawing.Circular
    private fun points(vararg values: Double) = values.mapIndexed { i, value -> WidgetPoint("Sample $i", value, null, null, null, null, null) }
    private fun parsed(kind: String, values: List<Double>): WidgetItem.Chart? {
        val points = JSONArray(values.mapIndexed { i, value -> JSONObject().put("label", "Sample $i").put("value", value)
            .put("open", 1).put("high", 4).put("low", 0).put("close", 3) })
        val chart = JSONObject().put("type", "chart").put("kind", kind)
            .put("series", JSONArray().put(JSONObject().put("name", "Sample").put("points", points)))
        val body = JSONObject().put("view", JSONObject().put("sections", JSONArray().put(JSONObject().put("items", JSONArray().put(chart)))))
        return ExtensionParser.view(body, "large").items.singleOrNull() as? WidgetItem.Chart
    }
}
