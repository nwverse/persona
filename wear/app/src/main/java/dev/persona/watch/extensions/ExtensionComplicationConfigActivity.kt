package dev.persona.watch.extensions

import android.app.Activity
import android.content.ComponentName
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.LinearLayout
import android.widget.ScrollView
import androidx.wear.watchface.complications.datasource.ComplicationDataSourceService
import dev.persona.watch.CredentialStore
import dev.persona.watch.HubDirectory
import dev.persona.watch.R
import dev.persona.watch.SavedHub
import dev.persona.watch.data.HubConfig
import dev.persona.watch.ui.WatchTheme as T
import java.util.concurrent.Executors

/** A system picker configures one explicit instance; choosing a widget never changes another face slot. */
class ExtensionComplicationConfigActivity : Activity() {
    private val io = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())
    private lateinit var body: LinearLayout
    private var instanceId = -1
    private var validRequest = false
    private var kind: WidgetFaceKind? = null
    private var group: SavedHub? = null
    private var groups = emptyList<SavedHub>()
    private val catalog = WidgetConfigCatalog()
    private var selected: ExtensionWidget? = null
    private var selectedView: ExtensionWidgetView? = null
    private var busy = false
    private var message = ""
    private var foreground = false
    private var generation = 0

    @Suppress("DEPRECATION")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setResult(RESULT_CANCELED)
        instanceId = intent.getIntExtra(ComplicationDataSourceService.EXTRA_CONFIG_COMPLICATION_ID, -1)
        kind = WidgetConfigContract.kind(intent.getIntExtra(ComplicationDataSourceService.EXTRA_CONFIG_COMPLICATION_TYPE, -1))
        val source = runCatching { intent.getParcelableExtra<ComponentName>(ComplicationDataSourceService.EXTRA_CONFIG_DATA_SOURCE_COMPONENT) }.getOrNull()
        if (instanceId < 0 || kind == null || source != ComponentName(this, ExtensionComplicationService::class.java)) {
            finish(); return
        }
        validRequest = true
        render()
    }

    override fun onResume() {
        super.onResume(); foreground = true
        if (validRequest) loadGroups()
    }
    override fun onPause() { foreground = false; generation++; busy = false; super.onPause() }
    override fun onDestroy() { generation++; io.shutdownNow(); main.removeCallbacksAndMessages(null); super.onDestroy() }

    private fun loadGroups() {
        val ticket = ++generation
        group = null; selected = null; selectedView = null; catalog.clear(); busy = true; message = ""; render()
        io.execute {
            val result = runCatching {
                listOfNotNull(CredentialStore(this).load()?.let { SavedHub("", getString(R.string.hub_primary), it) }) +
                    HubDirectory(this).additional().filter { it.config != null }
            }
            main.post {
                if (!accept(ticket)) return@post
                busy = false
                result.fold({ groups = it }, { groups = emptyList(); message = getString(R.string.extension_retryable) })
                render()
                if (groups.size == 1) loadCatalog(groups.single())
            }
        }
    }

    private fun loadCatalog(connection: SavedHub) {
        val config = connection.config ?: return
        val ticket = ++generation
        group = connection; selected = null; selectedView = null; catalog.clear(); busy = true; message = ""; render()
        io.execute {
            val result = runCatching {
                same(connection.id, config)
                val api = ExtensionClient(config, language())
                val found = WidgetConfigCatalog.Loaded.discover(api, checkNotNull(kind))
                same(connection.id, config)
                ExtensionComplications.store(this).saveCatalog(connection.id, config, found.allWidgets)
                ExtensionComplications.changed(this)
                found
            }.onFailure { invalidateRevoked(connection.id, config, it) }
            main.post {
                if (!accept(ticket)) return@post
                busy = false
                result.fold({ catalog.publish(it) }, { message = getString(problemText(it)) })
                render()
            }
        }
    }

    private fun preview(widget: ExtensionWidget) {
        val connection = group ?: return
        val config = connection.config ?: return
        // A revoked or aged client must rediscover support before another preview can be requested.
        val api = catalog.clientFor(widget) ?: run { loadCatalog(connection); return }
        val family = widget.familyFor(checkNotNull(kind)) ?: return
        val ticket = ++generation
        selected = widget; selectedView = null; busy = true; message = ""; render()
        io.execute {
            val result = runCatching {
                same(connection.id, config)
                val view = api.widget(widget, force = true, family = family)
                same(connection.id, config)
                check(WidgetFaceText.words(view, widget.title) != null) { "widget_empty" }
                check(ExtensionComplications.store(this).saveProjection(connection.id, config, widget, family, view)) { "widget_not_cached" }
                ExtensionComplications.changed(this)
                view
            }.onFailure { invalidateRevoked(connection.id, config, it, widget, family) }
            main.post {
                if (!accept(ticket)) return@post
                busy = false
                result.fold({ selectedView = it }, { message = getString(problemText(it)) })
                render()
            }
        }
    }

    private fun useSelection() {
        val connection = group ?: return
        val config = connection.config ?: return
        val widget = selected ?: return
        val type = kind ?: return
        val family = widget.familyFor(type) ?: return
        if (selectedView == null || busy) return
        // This final, explicit tap commits only a small local mapping. No provider action or face replacement runs.
        runCatching {
            same(connection.id, config)
            ExtensionComplications.store(this).bind(instanceId, connection.id, config, widget, family, type)
        }.fold({
            ExtensionComplications.changed(this)
            setResult(RESULT_OK); finish()
        }, { message = getString(R.string.extension_config_failed); render() })
    }

    private fun render() {
        val scroll = ScrollView(this).apply { setBackgroundColor(T.background); isFillViewport = true }
        body = T.column(this).apply { setPadding(dp(T.screenInset), dp(T.roundEndInset), dp(T.screenInset), dp(T.roundEndInset)) }
        scroll.addView(body); setContentView(scroll)
        text(getString(R.string.extension_choose_widget), T.title, true)
        if (busy) text(getString(R.string.loading), T.caption)
        if (message.isNotBlank()) text(message, T.caption)
        val widget = selected
        val connection = group
        when {
            widget != null -> {
                text(widget.title, T.heading, true)
                selectedView?.let { view -> WidgetFaceText.words(view, widget.title)?.let { words ->
                    text(words.title, T.caption); text(words.text, T.heading)
                    if (view.expiresAtMs <= System.currentTimeMillis()) text(getString(R.string.extension_stale), T.caption)
                } }
                text(getString(R.string.extension_face_refresh_detail), T.caption)
                if (selectedView != null) button(R.string.extension_use_widget) { useSelection() }
                else if (!busy) button(R.string.retry) { preview(widget) }
                button(R.string.back, true) { loadCatalog(checkNotNull(connection)) }
            }
            connection != null -> {
                text(connection.name, T.caption)
                if (!busy && catalog.widgets.isEmpty()) text(getString(R.string.extension_face_empty))
                catalog.widgets.forEach { entry ->
                    val title = T.text(this, entry.title, T.heading, bold = true).apply {
                        isAccessibilityHeading = true
                        setPadding(0, dp(T.lineGap), 0, dp(T.lineGap))
                    }
                    body.addView(title)
                    val label = getString(R.string.extension_preview_widget)
                    body.addView(T.button(this, label) { preview(entry) }.apply {
                        id = View.generateViewId()
                        title.labelFor = id
                        isEnabled = !busy
                        contentDescription = "$label: ${entry.title}"
                    })
                }
                if (!busy) button(R.string.refresh) { loadCatalog(connection) }
                if (groups.size > 1) button(R.string.hub_groups, true) { loadGroups() }
            }
            else -> {
                if (!busy && groups.isEmpty()) text(getString(R.string.extension_unpaired))
                groups.forEach { entry -> button(entry.name) { loadCatalog(entry) } }
            }
        }
        button(R.string.cancel, true) { setResult(RESULT_CANCELED); finish() }
    }

    private fun same(group: String, config: HubConfig) {
        if (ExtensionComplications.config(this, group) != config) throw ExtensionException(ExtensionProblem.CONNECTION_CHANGED)
    }
    private fun invalidateRevoked(group: String, config: HubConfig, failure: Throwable,
                                  widget: ExtensionWidget? = null, family: String? = null) {
        val problem = (failure as? ExtensionException)?.problem
        if (WidgetFailureCache.invalidates(problem)) {
            runCatching { WidgetFailureCache.invalidate(ExtensionComplications.store(this), group, config, problem, widget, family) }
            ExtensionComplications.changed(this)
        }
    }
    private fun accept(ticket: Int) = foreground && !isDestroyed && ticket == generation
    private fun problemText(failure: Throwable) = when ((failure as? ExtensionException)?.problem) {
        ExtensionProblem.AUTHENTICATION -> R.string.authentication_failed
        ExtensionProblem.FORBIDDEN -> R.string.extension_forbidden
        ExtensionProblem.UNAVAILABLE -> R.string.extension_unavailable
        ExtensionProblem.CONNECTION_CHANGED -> R.string.extension_connection_changed
        ExtensionProblem.INVALID_RESPONSE -> R.string.extension_invalid_response
        else -> R.string.extension_retryable
    }
    private fun text(value: String, size: Float = T.body, bold: Boolean = false) {
        if (value.isNotBlank()) body.addView(T.text(this, value, size, T.ink, bold).apply { setPadding(0, dp(T.lineGap), 0, dp(T.lineGap)) })
    }
    private fun button(id: Int, duringLoad: Boolean = false, action: () -> Unit) = button(getString(id), duringLoad, action)
    private fun button(label: String, duringLoad: Boolean = false, action: () -> Unit) {
        body.addView(T.button(this, label, onClick = action).apply { isEnabled = duringLoad || !busy; contentDescription = label })
    }
    private fun dp(value: Int) = T.dp(this, value)
    private fun language() = when (resources.configuration.locales[0].language) { "zh" -> "zh-Hans"; "ja" -> "ja"; else -> "en" }
}
