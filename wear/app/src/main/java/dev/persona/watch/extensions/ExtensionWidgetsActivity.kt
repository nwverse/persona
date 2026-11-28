package dev.persona.watch.extensions

import android.app.Activity
import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Path
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.window.OnBackInvokedCallback
import android.window.OnBackInvokedDispatcher
import dev.persona.watch.CredentialStore
import dev.persona.watch.HubDirectory
import dev.persona.watch.R
import dev.persona.watch.data.HubConfig
import dev.persona.watch.ui.WatchTheme as T
import java.text.NumberFormat
import java.util.Locale
import java.util.concurrent.Executors

/** Wrist-sized, read-only extension projections. Provider actions never become Android controls. */
class ExtensionWidgetsActivity : Activity() {
    private val io = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())
    private lateinit var body: LinearLayout
    private lateinit var scroll: ScrollView
    private lateinit var diskCache: ExtensionCacheStore
    private var group = ""
    private var deepLink: String? = null
    private var client: ExtensionClient? = null
    private var catalogReady = false
    private var config: HubConfig? = null
    private var widgets = emptyList<ExtensionWidget>()
    private var selected: ExtensionWidget? = null
    private var selectedKey: String? = null
    private var projection: CachedWidget? = null
    private val chartDetails = WidgetChartDetails()
    private var problem: ExtensionProblem? = null
    private var busy = false
    private var unregisterSystemBack: (() -> Unit)? = null
    @Volatile private var foreground = false
    @Volatile private var generation = 0
    private var expiryDeadline: Long? = null
    private val expire = object : Runnable {
        override fun run() {
            if (!foreground) return
            val deadline = expiryDeadline ?: return
            WidgetExpiry.wake(deadline, System.currentTimeMillis(),
                schedule = { main.postDelayed(this, it) },
                publish = { ExtensionComplications.changed(this@ExtensionWidgetsActivity) },
                render = { render(true) })
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = T.background
        window.navigationBarColor = T.background
        group = intent.getStringExtra("group").orEmpty()
        deepLink = intent.dataString
        diskCache = ExtensionComplications.store(this)
        selectedKey = savedInstanceState?.getString("widget") ?: intent.getStringExtra("widget_key")
        selectedKey?.let { key ->
            val segments = key.split('/')
            if (segments.size != 2 || segments.any { !ExtensionParser.validID(it) }) problem = ExtensionProblem.INVALID_LINK
        }
        if (group.isNotEmpty() && !group.matches(Regex("[a-f0-9-]{36}"))) problem = ExtensionProblem.INVALID_LINK
        if (deepLink != null && ExtensionLink.parse(deepLink!!, group) == null) problem = ExtensionProblem.INVALID_LINK
        render()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val callback = OnBackInvokedCallback { navigateBack() }
            val dispatcher = onBackInvokedDispatcher
            dispatcher.registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT, callback)
            unregisterSystemBack = { dispatcher.unregisterOnBackInvokedCallback(callback) }
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        outState.putString("widget", selected?.key ?: selectedKey)
        super.onSaveInstanceState(outState)
    }

    override fun onResume() {
        super.onResume()
        foreground = true
        if (problem != ExtensionProblem.INVALID_LINK) loadCatalog()
    }

    override fun onPause() {
        foreground = false
        generation++
        busy = false
        main.removeCallbacks(expire)
        super.onPause()
    }

    override fun onDestroy() {
        unregisterSystemBack?.invoke()
        unregisterSystemBack = null
        generation++
        io.shutdownNow()
        main.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    @Deprecated("Legacy system back; Android 13 and later use OnBackInvokedDispatcher")
    override fun onBackPressed() = navigateBack()

    private fun navigateBack() {
        if (isFinishing || isDestroyed) return
        generation++; busy = false
        when {
            chartDetails.value != null -> { chartDetails.clear(); render() }
            selected != null && deepLink == null -> { selected = null; selectedKey = null; projection = null; problem = null; render() }
            else -> finish()
        }
    }

    private fun savedConfig(): HubConfig? = if (group.isEmpty()) CredentialStore(this).load()
        else HubDirectory(this).additional().singleOrNull { it.id == group }?.config

    /** Every refresh rechecks the saved group, including after returning from another activity. */
    private fun loadCatalog() {
        if (busy || isDestroyed) return
        val ticket = ++generation
        busy = true
        catalogReady = false
        problem = null
        val previousConfig = config
        val previousClient = client
        val previousWidgets = widgets
        val previousProjection = projection
        val previousSelectedKey = selected?.key ?: selectedKey
        val requestedKey = previousSelectedKey ?: selectedKey
        // Hide cached private content until the connection identity has been checked off the UI thread.
        widgets = emptyList(); selected = null; projection = null; chartDetails.clear()
        render()
        io.execute {
            val result = runCatching {
                val saved = savedConfig()?.validated() ?: throw ExtensionException(ExtensionProblem.UNPAIRED)
                val same = saved == previousConfig
                val api = if (same) previousClient ?: ExtensionClient(saved, language()) else ExtensionClient(saved, language())
                val savedWidgets = runCatching { diskCache.catalog(group, saved) }.getOrNull().orEmpty()
                val savedWidget = deepLink?.let { ExtensionLink.parse(it, group)?.projection(savedWidgets) }
                    ?: savedWidgets.firstOrNull { it.key == requestedKey }
                val savedView = savedWidget?.let { runCatching { diskCache.projection(group, saved, it, it.family) }.getOrNull() }
                if (savedConfig() != saved) throw ExtensionException(ExtensionProblem.CONNECTION_CHANGED)
                val preview = recoverWidgetSelection(true, same, requestedKey,
                    previousWidgets, previousSelectedKey, previousProjection, savedWidgets, savedWidget, savedView)
                // Restore identity-checked local content before starting the potentially slow Hub request.
                main.post {
                    if (!foreground || isDestroyed || ticket != generation) return@post
                    if (!same) chartDetails.clear()
                    config = saved; client = api
                    widgets = preview.widgets; selected = preview.selected
                    selectedKey = preview.selectedKey; projection = preview.projection
                    chartDetails.reconcile(selected, projection)
                    render()
                }
                val catalogResult = runCatching { api.widgets() }
                if (savedConfig() != saved) throw ExtensionException(ExtensionProblem.CONNECTION_CHANGED)
                catalogResult.onSuccess { runCatching { diskCache.saveCatalog(group, saved, it) }; ExtensionComplications.changed(this) }
                    .onFailure { invalidateFailure(saved, it) }
                LoadResult(saved, api, same, catalogResult, savedWidgets, savedWidget, savedView)
            }
            main.post {
                if (!foreground || isDestroyed || ticket != generation) return@post
                busy = false
                result.fold({ loaded ->
                    if (!loaded.same) { selected = null; chartDetails.clear() }
                    config = loaded.config
                    client = loaded.client
                    loaded.catalog.fold({ found ->
                        catalogReady = true
                        widgets = found
                        selected = deepLink?.let { ExtensionLink.parse(it, group)?.projection(found) }
                            ?: found.firstOrNull { it.key == (selected?.key ?: selectedKey) }
                        if ((deepLink != null || requestedKey != null) && selected == null) problem = ExtensionProblem.UNAVAILABLE
                        projection = retainWidgetProjection(selected, loaded.same,
                            previousProjection.takeIf { previousSelectedKey == selected?.key },
                            loaded.savedView, System.currentTimeMillis())
                        chartDetails.reconcile(selected, projection)
                        render()
                        selected?.let { loadWidget(it, force = false) }
                    }, { failure ->
                        problem = problemOf(failure)
                        val mayKeep = problem == ExtensionProblem.RETRYABLE
                        val recovered = recoverWidgetSelection(mayKeep, loaded.same, requestedKey,
                            previousWidgets, previousSelectedKey, previousProjection,
                            loaded.savedWidgets, loaded.savedWidget, loaded.savedView)
                        widgets = recovered.widgets; selected = recovered.selected
                        selectedKey = recovered.selectedKey; projection = recovered.projection
                        chartDetails.reconcile(selected, projection)
                        // An earlier successful catalogue on this same client can authorize a retry.
                        // Otherwise show the selected cached/retry detail without trusting a disk snapshot.
                        catalogReady = mayKeep && loaded.same && selected?.let(loaded.client::canProject) == true
                        render()
                        if (catalogReady) selected?.let { loadWidget(it, force = false) }
                    })
                }, { failure ->
                    problem = problemOf(failure)
                    config = null; client = null; widgets = emptyList(); selected = null; projection = null; chartDetails.clear()
                    render()
                })
            }
        }
    }

    private fun loadWidget(widget: ExtensionWidget, force: Boolean) {
        if (busy) return
        projection = retainWidgetProjection(widget, true, projection, null, System.currentTimeMillis())
        selected = widget; selectedKey = widget.key; chartDetails.clear()
        // A disk snapshot cannot authorize a new projection: retry discovery after an offline catalogue.
        if (!catalogReady) { loadCatalog(); return }
        val api = client ?: return
        val expected = config ?: return
        val ticket = ++generation
        val previousProjection = projection
        busy = true; problem = null; render()
        io.execute {
            val result = runCatching {
                if (savedConfig() != expected) throw ExtensionException(ExtensionProblem.CONNECTION_CHANGED)
                api.projection(widget, force).also {
                    if (savedConfig() != expected) throw ExtensionException(ExtensionProblem.CONNECTION_CHANGED)
                    runCatching { diskCache.saveProjection(group, expected, widget, widget.family, it.view, it.savedAtMs) }
                    ExtensionComplications.changed(this)
                }
            }.onFailure { invalidateFailure(expected, it, widget, widget.family) }
            main.post {
                if (!foreground || isDestroyed || ticket != generation) return@post
                busy = false
                result.fold({ projection = it }, { failure ->
                    problem = problemOf(failure)
                    projection = previousProjection.takeIf { problem == ExtensionProblem.RETRYABLE }
                    if (problem in setOf(ExtensionProblem.AUTHENTICATION, ExtensionProblem.FORBIDDEN, ExtensionProblem.CONNECTION_CHANGED)) {
                        widgets = emptyList(); selected = null; selectedKey = null; chartDetails.clear()
                    }
                })
                render()
            }
            if (result.isSuccess) refreshConfiguredFaces(api, expected, widget, ticket)
        }
    }

    /** User-initiated foreground refresh also updates only the accessory families the user configured. */
    private fun refreshConfiguredFaces(api: ExtensionClient, expected: HubConfig, widget: ExtensionWidget, ticket: Int) {
        val families = runCatching { diskCache.configured(group, expected, widget).map { it.family }.distinct() }.getOrDefault(emptyList())
        for (family in families.filter { it != widget.family }) {
            if (!foreground || generation != ticket || savedConfig() != expected) return
            runCatching { api.widget(widget, force = true, family = family) }.onSuccess { view ->
                if (savedConfig() == expected) runCatching { diskCache.saveProjection(group, expected, widget, family, view) }
            }.onFailure { invalidateFailure(expected, it, widget, family) }
        }
        ExtensionComplications.changed(this)
    }

    private fun render(preserveScroll: Boolean = false) {
        val old = if (preserveScroll && ::scroll.isInitialized) scroll.scrollY else 0
        main.removeCallbacks(expire)
        expiryDeadline = null
        scroll = ScrollView(this).apply { setBackgroundColor(T.background); isFillViewport = true }
        body = T.column(this).apply { setPadding(dp(T.screenInset), dp(T.roundEndInset), dp(T.screenInset), dp(T.roundEndInset)) }
        scroll.addView(body); setContentView(scroll)
        text(selected?.title ?: getString(R.string.extension_widgets), T.title, bold = true)
        if (busy) text(getString(R.string.loading), T.caption, T.secondary)
        problem?.let { text(getString(problemString(it)), T.body, T.working) }
        chartDetails.reconcile(selected, projection)
        val values = chartDetails.value
        if (values != null) {
            values.series.forEach { series ->
                text(series.name, T.heading, bold = true)
                series.points.forEach { point -> text(pointText(point, values.unit, values.kind == "candlestick"), T.caption) }
            }
            button(getString(R.string.back), duringLoad = true) { navigateBack() }
        } else if (selected != null) {
            projection?.let { cached ->
                val view = cached.view
                if (view.title.isNotBlank() && view.title != selected?.title) text(view.title, T.heading, bold = true)
                val now = System.currentTimeMillis()
                val delay = if (cached.fresh(now)) WidgetExpiry.nextDelay(cached.freshUntilMs, now) else null
                if (delay == null)
                    text(getString(R.string.extension_stale), T.caption, T.working)
                else if (foreground) { expiryDeadline = cached.freshUntilMs; main.postDelayed(expire, delay) }
                if (view.items.isEmpty()) text(getString(R.string.extension_no_content), T.body, T.secondary)
                view.items.forEach(::item)
            }
            text(getString(R.string.extension_read_only), T.caption, T.secondary)
            button(getString(R.string.refresh)) { selected?.let { loadWidget(it, force = true) } }
            button(getString(R.string.back), duringLoad = true) { navigateBack() }
        } else {
            if (!busy && problem == null && widgets.isEmpty()) text(getString(R.string.extension_empty), T.body, T.secondary)
            widgets.forEach { widget ->
                val title = T.text(this, widget.title, T.heading, bold = true).apply {
                    isAccessibilityHeading = true
                    setPadding(T.zero, dp(T.lineGap), T.zero, dp(T.lineGap))
                }
                body.addView(title)
                val label = getString(R.string.extension_view_widget)
                body.addView(T.button(this, label) { projection = null; loadWidget(widget, force = false) }.apply {
                    id = View.generateViewId()
                    title.labelFor = id
                    isEnabled = !busy
                    contentDescription = "$label: ${widget.title}"
                })
                if (widget.source.isNotBlank()) text(widget.source, T.caption, T.secondary)
            }
            if (problem != ExtensionProblem.INVALID_LINK) button(getString(R.string.refresh)) { loadCatalog() }
            button(getString(R.string.back), duringLoad = true) { finish() }
        }
        scroll.post { scroll.scrollTo(0, old) }
    }

    private fun item(value: WidgetItem) {
        val card = T.column(this).apply {
            background = T.surface(this@ExtensionWidgetsActivity)
            setPadding(dp(T.padding), dp(T.gap), dp(T.padding), dp(T.gap))
            layoutParams = LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(T.gap) }
        }
        val outer = body
        body.addView(card); body = card
        when (value) {
            is WidgetItem.Text -> text(value.text)
            is WidgetItem.Metric -> metric(value.metric)
            is WidgetItem.Progress -> {
                text(value.label, T.caption, T.secondary)
                val percent = NumberFormat.getPercentInstance().format(value.ratio)
                text(percent, T.heading)
                body.addView(ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
                    max = ExtensionTokens.progressSteps; progress = (value.ratio * max).toInt()
                    progressTintList = android.content.res.ColorStateList.valueOf(T.accent)
                    contentDescription = "${value.label}: $percent"
                    layoutParams = LinearLayout.LayoutParams(-1, dp(ExtensionTokens.progressHeight))
                })
                text(value.detail, T.caption, T.secondary)
            }
            is WidgetItem.Row -> { text(value.title, T.heading); text(value.subtitle, T.caption, T.secondary); text(value.value) }
            is WidgetItem.Stats -> value.values.forEach(::metric)
            is WidgetItem.Chart -> chart(value)
        }
        body = outer
    }

    private fun metric(value: WidgetMetric) {
        text(value.label, T.caption, T.secondary)
        text(value.value, T.heading, bold = true)
        text(value.detail, T.caption, T.secondary)
    }

    private fun chart(value: WidgetItem.Chart) {
        text(value.title, T.heading)
        value.series.forEach { series ->
            text(series.name, T.caption, T.secondary)
            val drawing = WidgetChartDrawing.from(value.kind, series.points)
            if (drawing is WidgetChartDrawing.Values) drawing.points.forEach { text(pointText(it, value.unit, true), T.caption) }
            else if (series.points.isNotEmpty()) {
                body.addView(WidgetChartView(this, drawing).apply {
                    contentDescription = series.points.joinToString("\n") { pointText(it, value.unit, false) }
                    importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
                })
                text(pointText(series.points.last(), value.unit, false), T.caption)
            }
        }
        if (value.series.any { it.points.isNotEmpty() }) button(getString(R.string.extension_chart_values)) { chartDetails.open(selected, projection, value); render() }
    }

    private fun pointText(point: WidgetPoint, unit: String, candle: Boolean): String {
        if (candle) return getString(R.string.extension_candle_values, point.label, number(point.open), number(point.high), number(point.low), number(point.close))
        return "${point.label}: ${point.display ?: number(point.value)}$unit"
    }
    private fun number(value: Double?) = value?.let { String.format(Locale.getDefault(), "%.4g", it) } ?: getString(R.string.extension_no_value)
    private fun text(value: String, size: Float = T.body, color: Int = T.ink, bold: Boolean = false) {
        if (value.isBlank()) return
        body.addView(T.text(this, value, size, color, bold).apply {
            setPadding(T.zero, dp(T.lineGap), T.zero, dp(T.lineGap))
            setTextIsSelectable(true)
            if (bold) isAccessibilityHeading = true
        })
    }
    private fun button(label: String, duringLoad: Boolean = false, action: () -> Unit) {
        body.addView(T.button(this, label, onClick = action).apply { isEnabled = duringLoad || !busy; contentDescription = label })
    }
    private fun language() = when (resources.configuration.locales[0].language) { "zh" -> "zh-Hans"; "ja" -> "ja"; else -> "en" }
    private fun dp(value: Int) = T.dp(this, value)
    private fun invalidateFailure(expected: HubConfig, failure: Throwable, widget: ExtensionWidget? = null, family: String? = null) {
        val problem = problemOf(failure)
        if (WidgetFailureCache.invalidates(problem)) {
            runCatching { WidgetFailureCache.invalidate(diskCache, group, expected, problem, widget, family) }
            ExtensionComplications.changed(this)
        }
    }
    private fun problemOf(error: Throwable) = (error as? ExtensionException)?.problem ?: ExtensionProblem.RETRYABLE
    private fun problemString(problem: ExtensionProblem) = when (problem) {
        ExtensionProblem.UNPAIRED -> R.string.extension_unpaired
        ExtensionProblem.AUTHENTICATION -> R.string.authentication_failed
        ExtensionProblem.FORBIDDEN -> R.string.extension_forbidden
        ExtensionProblem.UNAVAILABLE -> R.string.extension_unavailable
        ExtensionProblem.RETRYABLE -> R.string.extension_retryable
        ExtensionProblem.INVALID_RESPONSE -> R.string.extension_invalid_response
        ExtensionProblem.INVALID_LINK -> R.string.extension_invalid_link
        ExtensionProblem.CONNECTION_CHANGED -> R.string.extension_connection_changed
    }
    private data class LoadResult(val config: HubConfig, val client: ExtensionClient, val same: Boolean, val catalog: Result<List<ExtensionWidget>>,
        val savedWidgets: List<ExtensionWidget>, val savedWidget: ExtensionWidget?, val savedView: CachedWidget?)
}

/** Small-multiple plot; full labelled values remain available without relying on color or hover. */
private class WidgetChartView(context: Context, private val drawing: WidgetChartDrawing) : View(context) {
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = T.ink; strokeWidth = T.dp(context, ExtensionTokens.chartLine).toFloat() }
    init { layoutParams = LinearLayout.LayoutParams(-1, T.dp(context, ExtensionTokens.chartHeight)) }
    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        if (width <= 0 || height <= 0) return
        val inset = T.dp(context, T.lineGap).toFloat()
        val spanX = (width - inset * 2).coerceAtLeast(1f)
        val spanY = (height - inset * 2).coerceAtLeast(1f)
        fun x(point: WidgetChartDrawing.Position) = inset + point.x * spanX
        fun y(value: Float) = inset + value * spanY
        paint.color = T.ink; paint.alpha = 255
        when (val plot = drawing) {
            is WidgetChartDrawing.Line -> {
                if (plot.points.isEmpty()) return
                val path = Path()
                plot.points.forEachIndexed { index, point -> if (index == 0) path.moveTo(x(point), y(point.y)) else path.lineTo(x(point), y(point.y)) }
                if (plot.filled) {
                    val fill = Path(path).apply { lineTo(x(plot.points.last()), y(plot.baseline)); lineTo(x(plot.points.first()), y(plot.baseline)); close() }
                    paint.style = Paint.Style.FILL; paint.alpha = ExtensionTokens.chartFillAlpha; canvas.drawPath(fill, paint); paint.alpha = 255
                }
                paint.style = Paint.Style.STROKE; canvas.drawPath(path, paint)
                if (plot.points.size == 1) { paint.style = Paint.Style.FILL; canvas.drawCircle(x(plot.points[0]), y(plot.points[0].y), paint.strokeWidth, paint) }
            }
            is WidgetChartDrawing.Bars -> {
                if (plot.points.isEmpty()) return
                paint.style = Paint.Style.FILL
                val half = minOf(T.dp(context, ExtensionTokens.chartBar).toFloat(), spanX / plot.points.size * ExtensionTokens.chartBarFraction) / 2
                plot.points.forEach { point -> canvas.drawRect(x(point) - half, minOf(y(point.y), y(plot.baseline)), x(point) + half, maxOf(y(point.y), y(plot.baseline)), paint) }
            }
            is WidgetChartDrawing.Circular -> {
                val radius = minOf(spanX, spanY) / 2
                val centerX = width / 2f
                val centerY = height / 2f
                val arc = plot.geometry(radius, ExtensionTokens.chartDonutHole)
                paint.style = if (arc.useCenter) Paint.Style.FILL else Paint.Style.STROKE
                paint.strokeWidth = arc.strokeWidth
                val colors = intArrayOf(T.accent, T.positive, T.working, T.brand, T.secondary)
                plot.slices.forEach { slice ->
                    paint.color = colors[slice.pointIndex % colors.size]
                    canvas.drawArc(centerX - arc.radius, centerY - arc.radius, centerX + arc.radius, centerY + arc.radius,
                        slice.start, slice.sweep, arc.useCenter, paint)
                }
            }
            is WidgetChartDrawing.Values -> Unit
        }
    }
}

/** Watch-specific counterparts of ExtensionSurfaceTokens; no extension controls share this surface. */
private object ExtensionTokens {
    const val chartHeight = 72
    const val chartLine = 2
    const val chartBar = 6
    const val chartBarFraction = 0.7f
    const val chartDonutHole = 0.55f
    const val chartFillAlpha = 42
    const val progressHeight = 8
    const val progressSteps = 1000
}
