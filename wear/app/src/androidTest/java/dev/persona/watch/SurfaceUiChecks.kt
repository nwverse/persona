package dev.persona.watch

import android.app.Activity
import android.app.Instrumentation
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Rect
import android.graphics.drawable.BitmapDrawable
import android.os.Bundle
import android.os.SystemClock
import android.provider.Settings
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.view.accessibility.AccessibilityNodeInfo
import android.widget.ImageView
import android.widget.Button
import android.widget.TextView
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubConfig
import dev.persona.watch.extensions.ExtensionWidgetsActivity
import dev.persona.watch.extensions.ExtensionComplications
import dev.persona.watch.extensions.WidgetItem
import dev.persona.watch.pet.PetActivity
import dev.persona.watch.pet.PetPack
import dev.persona.watch.ui.WatchTheme
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.text.NumberFormat

/** Exercises the app's actual renderers against a loopback fixture, without configuring any watch face. */
class SurfaceUiChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext
    private val automation get() = instrumentation.uiAutomation
    private lateinit var config: HubConfig
    private var activity: Activity? = null
    private var deadline = 0L
    private var step = "guard"

    fun run(): Bundle {
        check(BuildConfig.DEBUG) { "surfaces_requires_debug" }
        config = checkNotNull(CredentialStore(context).load()) { "surfaces_connection_required" }.validated()
        requireFixture()
        val started = SystemClock.elapsedRealtime()
        deadline = started + TOTAL_TIMEOUT_MS
        sameFixture()
        check(request("/api/v1/health").optString("instance").isNotBlank()) { "surfaces_fixture_instance_required" }
        val snapshot = HubClient(config, "en").refresh()
        check(snapshot.machines.isNotEmpty() && snapshot.machines.all { it.id == "fixture" } &&
            snapshot.sessions.isNotEmpty() && snapshot.sessions.all { it.machineId == "fixture" }) { "surfaces_fixture_data_required" }
        var originalFailure: Throwable? = null
        try {
            control(JSONObject().put("reset", true).put("scenario", "surfaces").put("clearEvents", true))
            status("pet_assets_and_decoding")
            open(PetActivity::class.java)
            val pack = PetPack(context.cacheDir, config)
            waitUntil("verified_pet_package", 60_000) { pack.cached(verify = true)?.assets?.size == 13 }
            waitText(context.getString(R.string.pet_needs_you, 5))
            centerPet("surface-pet-ready-centered")
            waitUntil("decoded_pet_bitmap", 30_000) { petBitmapHash() != null }
            capture("surface-pet-ready")
            centerPet("surface-pet-animation-before")
            val motionEnabled = Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) > 0f
            val frames = mutableSetOf<Int>()
            if (motionEnabled) waitUntil("animated_pet_frames", 12_000) {
                petBitmapHash()?.let(frames::add)
                frames.size >= 3
            }
            click(text(R.string.pet_tap))
            // Pending work is deterministic in this fixture. A tap replays that state instead of inventing idle status.
            centerPet("surface-pet-animation-after-tap")
            waitUntil("pet_after_tap", 15_000) { petBitmapHash() != null }
            val tappedFrames = mutableSetOf<Int>()
            if (motionEnabled) waitUntil("animated_pet_after_tap", 12_000) {
                petBitmapHash()?.let(tappedFrames::add)
                tappedFrames.size >= 3
            }
            waitText(context.getString(R.string.pet_needs_you, 5))
            capture("surface-pet-tapped")

            status("widget_catalog_and_projection")
            open(ExtensionWidgetsActivity::class.java)
            waitText("Sample status widget")
            assertCatalogButtonLayouts()
            capture("surface-widget-catalog")
            click(widgetAction("Sample status widget"))
            waitText("Sample read-only status.")
            for (value in listOf("Sample count", "7", "Seven synthetic tasks", "Sample progress", "Sample progress: ${NumberFormat.getPercentInstance().format(0.5)}",
                "Half of sample work", "Sample next step", "Review on computer", "Queued", "Sample open", "2", "Sample done", "5",
                "Sample trend", "Sample series", "Wed: 3 units")) waitText(value)
            assertNoExtensionControls()
            capture("surface-widget-projection")
            click(text(R.string.extension_chart_values))
            for (value in listOf("Mon: 1 units", "Tue: 2 units", "Wed: 3 units")) waitText(value)
            capture("surface-widget-chart-values")
            back()
            waitText("Sample read-only status.")

            status("all_widget_chart_kinds")
            val projectionStore = ExtensionComplications.store(context)
            val selectedWidget = checkNotNull(projectionStore.catalog("", config)?.singleOrNull { it.id == "overview" })
            fun projection() = projectionStore.projection("", config, selectedWidget, selectedWidget.family)
            for (kind in listOf("line", "area", "bar", "pie", "donut", "candlestick")) {
                control(JSONObject().put("chartKind", kind))
                val previousDate = checkNotNull(projection()).savedAtMs
                click(text(R.string.refresh))
                waitUntil("widget_chart_$kind") {
                    projection()?.let { current -> current.savedAtMs > previousDate &&
                        current.view.items.filterIsInstance<WidgetItem.Chart>().singleOrNull()?.let {
                            it.kind == kind && it.series.isNotEmpty() && it.series.all { series -> series.points.isNotEmpty() }
                        } == true } == true
                }
                waitText("Sample trend")
                waitText("Sample series")
                val candleRow = if (kind == "candlestick") {
                    val values = listOf(1.0, 3.0, 0.0, 2.0).map { String.format(java.util.Locale.getDefault(), "%.4g", it) }
                    context.getString(R.string.extension_candle_values, "Mon", values[0], values[1], values[2], values[3])
                } else null
                if (candleRow != null) waitText(candleRow) else waitText("Mon: 1 units\nTue: 2 units\nWed: 3 units")
                capture("surface-chart-$kind")
                viewportCapture().captureCentered("surface-chart-$kind-centered") { view ->
                    if (candleRow == null) view.javaClass.simpleName == "WidgetChartView"
                    else (view as? android.widget.TextView)?.text?.toString() == candleRow
                }
            }
            control(JSONObject().put("chartKind", "line"))
            val previousDate = checkNotNull(projection()).savedAtMs
            click(text(R.string.refresh))
            waitUntil("widget_fresh_retry_precondition") { projection()?.let {
                it.savedAtMs > previousDate && it.fresh(System.currentTimeMillis()) &&
                    it.view.items.filterIsInstance<WidgetItem.Chart>().singleOrNull()?.kind == "line"
            } == true }
            val beforeFailure = checkNotNull(projection())

            status("widget_transient_error_and_retry")
            control(JSONObject().put("failNext", JSONObject().put("path", OVERVIEW_PATH).put("status", 503).put("family", selectedWidget.family)))
            click(text(R.string.refresh))
            waitText(text(R.string.extension_retryable))
            waitText("Sample read-only status.")
            check(beforeFailure.fresh(System.currentTimeMillis())) { "surfaces_retry_freshness_window_elapsed" }
            check(projection() == beforeFailure) { "surfaces_retry_changed_cached_projection" }
            check(nodes().none { matches(it, text(R.string.extension_stale)) }) { "surfaces_retry_falsely_stale" }
            capture("surface-widget-fresh-retry-error")
            click(text(R.string.refresh))
            waitUntil("widget_retry_success") {
                val current = nodes()
                current.any { matches(it, "Sample read-only status.") } && current.none {
                    matches(it, text(R.string.extension_retryable)) || matches(it, text(R.string.loading))
                }
            }
            waitText("Sample read-only status.")
            capture("surface-widget-retry")

            status("unsupported_widget_items")
            back()
            waitText("Sample status widget")
            assertCatalogButtonLayouts()
            click(widgetAction("Sample unsupported widget"))
            waitText(text(R.string.extension_no_content))
            assertNoExtensionControls()
            capture("surface-widget-unsupported")

            status("catalog_unavailable_and_retry")
            control(JSONObject().put("failNext", JSONObject().put("path", CATALOG_PATH).put("status", 404)))
            open(ExtensionWidgetsActivity::class.java)
            waitText(text(R.string.extension_unavailable))
            check(nodes().none { matches(it, "Sample status widget") }) { "surfaces_unavailable_catalog_retained" }
            capture("surface-widget-unavailable")
            click(text(R.string.refresh))
            waitText("Sample status widget")
            assertCatalogButtonLayouts()
            capture("surface-widget-catalog-recovered")

            val events = control().getJSONArray("events")
            val rows = (0 until events.length()).map(events::getJSONObject)
            check(rows.any { it.optString("action") == "pet_asset" && it.optString("path").endsWith("/pet.json") }) {
                "surfaces_pet_manifest_not_fetched"
            }
            check(rows.count { it.optString("action") == "widget_projection" && it.optString("id") == "overview" &&
                it.optString("family") == "large" } >= 2) { "surfaces_projection_retry_not_observed" }
            check(rows.any { it.optString("path") == OVERVIEW_PATH && it.optInt("status") == 503 } &&
                rows.any { it.optString("path") == CATALOG_PATH && it.optInt("status") == 404 }) { "surfaces_faults_not_observed" }
            check(rows.none { it.optString("method") != "GET" && it.optString("path") !in setOf("/__test", OVERVIEW_PATH, UNSUPPORTED_PATH) }) {
                "surfaces_unexpected_external_action"
            }
            sameFixture()
            return Bundle().apply {
                putBoolean("surfaces_passed", true)
                putInt("pet_verified_assets", 13)
                putInt("pet_observed_frames", frames.size)
                putInt("pet_observed_tapped_frames", tappedFrames.size)
                putBoolean("pet_motion_enabled", motionEnabled)
                putInt("widget_chart_kinds", 6)
                putBoolean("system_complication_tested", false)
                putLong("surfaces_elapsed_ms", SystemClock.elapsedRealtime() - started)
                putString("stream", "Synthetic app surfaces passed: original pet assets and rendering, read-only widgets, chart values, unsupported content and retry. Watch-face installation was not tested.\n")
            }
        } catch (failure: Throwable) {
            originalFailure = failure
            runCatching { screenshot("surface-failure-$step") }
            throw failure
        } finally {
            val cleanup = runCatching { request("/__test", JSONObject().put("mode", "online").put("failNext", JSONObject.NULL)) }.exceptionOrNull()
            activity?.let { current -> instrumentation.runOnMainSync {
                if (!current.isDestroyed) current.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            } }
            if (cleanup != null) {
                val failure = originalFailure
                if (failure != null) failure.addSuppressed(cleanup) else throw cleanup
            }
        }
    }

    private fun requireFixture() {
        val uri = URI(config.baseUrl)
        check(config.token == FIXTURE_TOKEN && uri.scheme == "http" && uri.host in setOf("127.0.0.1", "localhost", "::1", "[::1]") &&
            uri.path.orEmpty() in setOf("", "/") && uri.userInfo == null && uri.query == null && uri.fragment == null) {
            "surfaces_loopback_fixture_required"
        }
    }
    private fun sameFixture() {
        check(CredentialStore(context).load() == config) { "surfaces_connection_changed" }
        check(HubDirectory(context).additional().isEmpty()) { "surfaces_existing_groups_untouched" }
        check(SystemClock.elapsedRealtime() < deadline) { "surfaces_total_timeout" }
    }
    private fun request(path: String, payload: JSONObject? = null): JSONObject {
        requireFixture()
        check(path in setOf("/api/v1/health", "/__test")) { "surfaces_control_path" }
        val connection = URL(config.baseUrl + path).openConnection() as HttpURLConnection
        try {
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 2_000; connection.readTimeout = 3_000
            connection.requestMethod = if (payload == null) "GET" else "POST"
            connection.setRequestProperty("Authorization", "Bearer $FIXTURE_TOKEN")
            if (payload != null) {
                val bytes = payload.toString().toByteArray(Charsets.UTF_8)
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
                connection.setFixedLengthStreamingMode(bytes.size)
                connection.outputStream.use { it.write(bytes) }
            }
            check(connection.responseCode == 200) { "surfaces_control_unavailable" }
            return JSONObject(connection.inputStream.bufferedReader(Charsets.UTF_8).use { it.readText() }).also {
                check(it.optBoolean("fixture")) { "surfaces_fixture_marker_required" }
            }
        } finally { connection.disconnect() }
    }
    private fun control(payload: JSONObject? = null): JSONObject { sameFixture(); return request("/__test", payload) }
    private fun open(type: Class<out Activity>) {
        sameFixture()
        activity?.let { old -> instrumentation.runOnMainSync { if (!old.isDestroyed) old.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) } }
        val current = instrumentation.startActivitySync(Intent(context, type).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK))
        activity = current
        instrumentation.runOnMainSync { current.window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) }
        waitUntil("app_window") { automation.rootInActiveWindow?.packageName?.toString() == context.packageName }
    }
    private fun back() { sameFixture(); instrumentation.sendKeyDownUpSync(KeyEvent.KEYCODE_BACK); SystemClock.sleep(300) }

    /** Inspect pixels already decoded and displayed by PetActivity, on its UI thread before any recycling. */
    private fun petBitmapHash(): Int? {
        var result: Int? = null
        instrumentation.runOnMainSync {
            fun find(view: View): ImageView? {
                if (view is ImageView && view.contentDescription?.toString() == text(R.string.pet_tap)) return view
                if (view is ViewGroup) for (index in 0 until view.childCount) find(view.getChildAt(index))?.let { return it }
                return null
            }
            val art = activity?.window?.decorView?.let(::find)
            val bitmap = (art?.drawable as? BitmapDrawable)?.bitmap
            val visible = Rect()
            if (art != null && art.isShown && art.getGlobalVisibleRect(visible) &&
                visible.width() == art.width && visible.height() == art.height &&
                bitmap != null && !bitmap.isRecycled && bitmap.width > 0 && bitmap.height > 0) {
                val pixels = IntArray(bitmap.width * bitmap.height)
                bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
                if (pixels.any { it ushr 24 != 0 }) result = pixels.contentHashCode()
            }
        }
        return result
    }
    private fun assertNoExtensionControls() {
        check(nodes().none { node -> node.isEditable || listOf("Never execute sample action", "Never edit sample setting", "Never open sample website")
            .any { matches(node, it) } }) { "surfaces_extension_controls_exposed" }
    }
    private fun widgetAction(title: String) = "${text(R.string.extension_view_widget)}: $title"

    /** Inspect both the short action and its associated, fully wrapped title on real native views. */
    private fun assertCatalogButtonLayouts() = waitUntil("catalog_button_layouts") {
        var result: Result<Boolean>? = null
        instrumentation.runOnMainSync { result = runCatching {
            val views = mutableListOf<TextView>()
            fun visit(view: View) {
                if (view is TextView) views += view
                if (view is ViewGroup) for (index in 0 until view.childCount) visit(view.getChildAt(index))
            }
            visit(checkNotNull(activity).window.decorView)
            val titles = listOf("Sample status widget", "Sample unsupported widget")
            val catalog = titles.map { title ->
                val button = views.filterIsInstance<Button>().singleOrNull {
                    it.contentDescription?.toString() == widgetAction(title)
                } ?: return@runCatching false
                val heading = views.singleOrNull {
                    it !is Button && it.text.toString() == title && it.labelFor == button.id
                } ?: return@runCatching false
                button to heading
            }
            if (catalog.flatMap { listOf(it.first, it.second) }.any {
                it.isLayoutRequested || it.layout == null || it.width <= 0 || it.height <= 0
            }) return@runCatching false
            val minimum = WatchTheme.dp(context, WatchTheme.touch)
            catalog.forEachIndexed { index, (button, heading) ->
                val label = text(R.string.extension_view_widget)
                val layout = checkNotNull(button.layout)
                check(button.text.toString() == label && button.contentDescription?.toString() == widgetAction(titles[index])) {
                    "surfaces_catalog_button_label_$index"
                }
                check(button.minimumHeight >= minimum && button.width >= minimum && button.height >= minimum) {
                    "surfaces_catalog_button_touch_$index"
                }
                check(button.maxLines == 1 && layout.lineCount == 1 && layout.getEllipsisCount(0) == 0 &&
                    layout.getLineStart(0) == 0 && layout.getLineEnd(0) == label.length &&
                    layout.getLineWidth(0) <= button.width - button.compoundPaddingLeft - button.compoundPaddingRight + 1f) {
                    "surfaces_catalog_button_truncated_$index"
                }
                val titleLayout = checkNotNull(heading.layout)
                check(heading.isAccessibilityHeading && titleLayout.lineCount > 0 &&
                    titleLayout.getLineStart(0) == 0 && titleLayout.getLineEnd(titleLayout.lineCount - 1) == titles[index].length &&
                    (0 until titleLayout.lineCount).all {
                        titleLayout.getEllipsisCount(it) == 0 &&
                            titleLayout.getLineMax(it) <= heading.width - heading.compoundPaddingLeft - heading.compoundPaddingRight + 1f
                    } && heading.height >= titleLayout.height + heading.compoundPaddingTop + heading.compoundPaddingBottom) {
                    "surfaces_catalog_title_truncated_$index"
                }
            }
            true
        } }
        checkNotNull(result).getOrThrow()
    }
    private fun click(label: String) {
        var button: AccessibilityNodeInfo? = null
        waitUntil("click_target") {
            val target = nodes().firstOrNull { matches(it, label) && it.isEnabled && it.isVisibleToUser && clickable(it) != null }
            if (target == null) { revealOrScroll { matches(it, label) }; false } else { button = clickable(target); true }
        }
        sameFixture()
        check(checkNotNull(button).performAction(AccessibilityNodeInfo.ACTION_CLICK)) { "surfaces_click_failed" }
        SystemClock.sleep(250)
    }
    private fun waitText(value: String) = waitUntil("text") {
        if (nodes().any { it.isVisibleToUser && matches(it, value) }) true else { revealOrScroll { matches(it, value) }; false }
    }
    private fun revealOrScroll(match: (AccessibilityNodeInfo) -> Boolean) {
        val current = nodes()
        if (current.firstOrNull(match)?.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_SHOW_ON_SCREEN.id) == true) return
        current.firstOrNull { it.isScrollable && it.isVisibleToUser }?.performAction(AccessibilityNodeInfo.ACTION_SCROLL_FORWARD)
    }
    private fun clickable(node: AccessibilityNodeInfo): AccessibilityNodeInfo? {
        var current: AccessibilityNodeInfo? = node
        repeat(8) {
            val item = current ?: return null
            if (item.isClickable && item.isEnabled && item.packageName?.toString() == context.packageName) return item
            current = item.parent
        }
        return null
    }
    private fun nodes(): List<AccessibilityNodeInfo> {
        val root = automation.rootInActiveWindow ?: return emptyList()
        if (root.packageName?.toString() != context.packageName) return emptyList()
        val rows = mutableListOf<AccessibilityNodeInfo>()
        fun visit(node: AccessibilityNodeInfo, depth: Int) {
            if (depth > 30 || rows.size >= 1_000) return
            rows += node
            for (index in 0 until node.childCount) node.getChild(index)?.let { visit(it, depth + 1) }
        }
        visit(root, 0)
        return rows
    }
    private fun matches(node: AccessibilityNodeInfo, value: String) = node.text?.toString() == value || node.contentDescription?.toString() == value
    private fun waitUntil(reason: String, timeout: Long = 20_000, condition: () -> Boolean) {
        val end = minOf(deadline, SystemClock.elapsedRealtime() + timeout)
        while (SystemClock.elapsedRealtime() < end) {
            sameFixture()
            if (condition()) return
            SystemClock.sleep(200)
        }
        throw AssertionError("surfaces_${step}_${reason}_timeout")
    }
    private fun screenshot(name: String) {
        sameFixture()
        check(automation.rootInActiveWindow?.packageName?.toString() == context.packageName) { "surfaces_screenshot_window" }
        val bitmap = checkNotNull(automation.takeScreenshot()) { "surfaces_screenshot_failed" }
        try { saveScreenshot(name, bitmap) } finally { bitmap.recycle() }
    }
    private fun viewportCapture() = ViewportCapture(instrumentation,
        currentActivity = { checkNotNull(activity) }, check = { sameFixture() }, save = ::saveScreenshot)
    private fun centerPet(name: String) = viewportCapture().captureCentered(name) {
        it is ImageView && it.contentDescription?.toString() == text(R.string.pet_tap)
    }
    private fun capture(name: String) = viewportCapture().capture(name)
    private fun saveScreenshot(name: String, bitmap: Bitmap) {
        val folder = File(context.filesDir, "fixture-qa").apply { mkdirs() }
        File(folder, "$name.png").outputStream().use { check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) { "surfaces_screenshot_write_failed" } }
    }
    private fun text(id: Int) = context.getString(id)
    private fun status(value: String) {
        step = value
        instrumentation.sendStatus(0, Bundle().apply {
            putString("surfaces_step", value)
            putString("stream", "Synthetic app surfaces: $value\n")
        })
    }
    companion object {
        private const val FIXTURE_TOKEN = "persona-wear-fixture-only"
        private const val TOTAL_TIMEOUT_MS = 240_000L
        private const val CATALOG_PATH = "/api/v1/extensions/-/widgets"
        private const val OVERVIEW_PATH = "/api/v1/extensions/fixture-tools/-/widget/overview"
        private const val UNSUPPORTED_PATH = "/api/v1/extensions/fixture-tools/-/widget/unsupported"
    }
}
