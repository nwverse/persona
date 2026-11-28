package dev.persona.watch.extensions

import kotlin.math.abs

/** The same bounded drawing plan drives the native view and host-side geometry checks. */
internal sealed interface WidgetChartDrawing {
    data class Position(val x: Float, val y: Float)
    data class Line(val points: List<Position>, val baseline: Float, val filled: Boolean) : WidgetChartDrawing
    data class Bars(val points: List<Position>, val baseline: Float) : WidgetChartDrawing
    data class Slice(val pointIndex: Int, val start: Float, val sweep: Float)
    data class ArcGeometry(val radius: Float, val strokeWidth: Float, val useCenter: Boolean)
    data class Circular(val slices: List<Slice>, val donut: Boolean) : WidgetChartDrawing {
        fun geometry(outerRadius: Float, holeFraction: Float): ArcGeometry {
            if (!donut) return ArcGeometry(outerRadius, 0f, true)
            val stroke = outerRadius * (1 - holeFraction)
            return ArcGeometry(outerRadius - stroke / 2, stroke, false)
        }
    }
    data class Values(val points: List<WidgetPoint>) : WidgetChartDrawing

    companion object {
        fun from(kind: String, points: List<WidgetPoint>): WidgetChartDrawing = when (kind) {
            "line" -> cartesian(points, line = true, filled = false)
            "area" -> cartesian(points, line = true, filled = true)
            "bar" -> cartesian(points, line = false, filled = false)
            "pie", "donut" -> Circular(slices(points), donut = kind == "donut")
            // Keep OHLC values readable rather than squeezing candle labels into a small plot.
            "candlestick" -> Values(points.take(4))
            else -> throw IllegalArgumentException("Unsupported chart kind")
        }

        private fun cartesian(points: List<WidgetPoint>, line: Boolean, filled: Boolean): WidgetChartDrawing {
            val scale = (points.maxOfOrNull { abs(it.value) } ?: 0.0).coerceAtLeast(1.0)
            val normalized = points.map { it.value / scale }
            val low = (normalized.minOrNull() ?: 0.0).let { if (line) it else minOf(0.0, it) }
            val high = (normalized.maxOrNull() ?: 0.0).let { if (line) it else maxOf(0.0, it) }
            fun y(value: Double) = (if (high == low) 0.5 else 1.0 - (value - low) / (high - low)).toFloat()
            val positions = normalized.mapIndexed { index, value -> Position((index + 0.5f) / points.size, y(value)) }
            return if (line) Line(positions, y(low), filled) else Bars(positions, y(0.0))
        }

        private fun slices(points: List<WidgetPoint>): List<Slice> {
            // A zero/negative value contributes no slice; the accessible value list still retains it.
            // Normalize before adding so even several Double.MAX_VALUE values have a finite total.
            val positive = points.withIndex().filter { it.value.value > 0.0 && it.value.value.isFinite() }
            val scale = positive.maxOfOrNull { it.value.value } ?: return emptyList()
            val total = positive.sumOf { it.value.value / scale }
            var used = 0.0
            return positive.mapIndexed { index, point ->
                val sweep = if (index == positive.lastIndex) (360.0 - used).coerceAtLeast(0.0) else point.value.value / scale / total * 360.0
                Slice(point.index, (used - 90.0).toFloat(), sweep.toFloat()).also { used += sweep }
            }
        }
    }
}
