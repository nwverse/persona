package persona.platform.capsule

import android.app.AppOpsManager
import android.app.KeyguardManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.res.Configuration
import android.graphics.PixelFormat
import android.content.pm.ApplicationInfo
import android.util.Log
import android.hardware.display.DisplayManager
import android.view.Display
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.view.Gravity
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.view.WindowInsets
import android.view.WindowManager
import android.view.inputmethod.InputMethodManager
import android.widget.FrameLayout
import androidx.compose.runtime.*
import androidx.compose.foundation.layout.heightIn
import androidx.compose.ui.platform.ComposeView
import androidx.compose.ui.platform.ViewCompositionStrategy
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.setViewTreeLifecycleOwner
import androidx.lifecycle.setViewTreeViewModelStoreOwner
import androidx.savedstate.SavedStateRegistryController
import androidx.savedstate.SavedStateRegistryOwner
import androidx.savedstate.setViewTreeSavedStateRegistryOwner
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import persona.core.designsystem.AppStyleStore
import persona.core.designsystem.CapsuleTokens
import persona.core.designsystem.PersonaTheme
import kotlin.math.abs
import kotlin.math.roundToInt

/** One application-owned window; the app keeps its connection resident while the capsule is on. Call on the main thread. */
class CapsuleHost(
    context: Context,
    private val preferences: CapsulePreferences,
    private val source: CapsuleContentSource,
    private val extraContent: @Composable (CapsuleEntry?) -> Unit = {},
    private val theme: @Composable (@Composable () -> Unit) -> Unit = { content -> content() },
    /** The user is about to look at hub content (unlocked with the capsule usable, or expanded it): validate the link. */
    private val wake: () -> Unit = {},
    private val drafts: androidx.compose.runtime.snapshots.SnapshotStateMap<Pair<String?, String?>, String> = mutableStateMapOf(),
    private val interaction: () -> Unit = {},
    /** Turns the capsule off from its own menu; the app owns the setting (it also drops the resident connection). */
    private val turnOff: () -> Unit = { preferences.setMode(CapsuleMode.Off) },
) : AutoCloseable {
    private val app = context.applicationContext
    private val windowContext = app.createDisplayContext(app.getSystemService(DisplayManager::class.java).getDisplay(Display.DEFAULT_DISPLAY))
        .createWindowContext(WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY, null)
    private val windows = windowContext.getSystemService(WindowManager::class.java)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val completions = CapsuleCompletionQueue(scope)
    private val mutableVisible = MutableStateFlow(false)
    val visible = mutableVisible.asStateFlow()
    private val mutableAttention = MutableStateFlow(false)
    /** Enabled, permitted, screen on and unlocked: the capsule may be used at any moment, whether or not it shows. */
    val attention = mutableAttention.asStateFlow()
    private var keyguardWatch: Job? = null
    private val appOps = app.getSystemService(AppOpsManager::class.java)
    private val main = Handler(Looper.getMainLooper())
    private var root: CapsuleWindow? = null
    private var composition: ComposeView? = null
    private var registered = false
    private var dragging = false
    private var owner: WindowOwner? = null
    private var params: WindowManager.LayoutParams? = null
    private var expanded by mutableStateOf(false)
    private var windowFocused by mutableStateOf(false)
    private var announcedNotice: String? = null
    private var latest by mutableStateOf(source.content.value)
    private var foreground = true
    private var started = false
    private var closed = false
    private var lastLayout: List<Int>? = null
    private var imeTop: Int? = null
    private var maxHeight by mutableStateOf(CapsuleTokens.cardMaxHeight)
    private var position = preferences.dock
    private val style = AppStyleStore(app)
    private val permissionChanged = AppOpsManager.OnOpChangedListener { _, packageName ->
        if (packageName == app.packageName) main.post { refreshPermission() }
    }
    private val configurationChanged = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) { reconcile(); place() }
    }

    fun start() {
        check(!closed)
        if (started) return
        started = true
        appOps.startWatchingMode(AppOpsManager.OPSTR_SYSTEM_ALERT_WINDOW, app.packageName, permissionChanged)
        ContextCompat.registerReceiver(app, configurationChanged, IntentFilter(Intent.ACTION_CONFIGURATION_CHANGED).apply {
            addAction(Intent.ACTION_SCREEN_OFF); addAction(Intent.ACTION_SCREEN_ON); addAction(Intent.ACTION_USER_PRESENT)
        }, ContextCompat.RECEIVER_NOT_EXPORTED)
        scope.launch { completions.current.collect { reconcile() } }
        scope.launch {
            source.completions.collect { completion ->
                completions.setVisible(allowed())
                if (latest.noticeTargetIds?.contains(completion.targetId) != false) completions.offer(completion)
            }
        }
        scope.launch {
            combine(source.content, preferences.mode) { content, _ -> content }.collectLatest {
                latest = it
                reconcile()
            }
        }
    }

    fun expand() { changeExpanded(true) }

    fun setForeground(value: Boolean) { foreground = value; reconcile() }
    fun refreshPermission() { if (!closed) reconcile() }

    private fun allowed(): Boolean = started && !closed && latest.available && !foreground &&
        preferences.mode.value != CapsuleMode.Off && CapsulePermission.granted(app) &&
        app.getSystemService(PowerManager::class.java).isInteractive &&
        !app.getSystemService(KeyguardManager::class.java).isKeyguardLocked

    private fun usable(): Boolean = started && !closed && preferences.mode.value != CapsuleMode.Off &&
        CapsulePermission.granted(app) && app.getSystemService(PowerManager::class.java).isInteractive &&
        !app.getSystemService(KeyguardManager::class.java).isKeyguardLocked

    /**
     * USER_PRESENT is the only unlock signal, and some devices deliver it late or not at all (face unlock that keeps
     * the screen on, OEM keyguards). While the screen is on but locked, look again until it unlocks, so the capsule
     * returns right after unlocking instead of after the next content change.
     */
    private fun watchKeyguard() {
        if (keyguardWatch?.isActive == true) return
        keyguardWatch = scope.launch {
            val power = app.getSystemService(PowerManager::class.java)
            val keyguard = app.getSystemService(KeyguardManager::class.java)
            repeat(KEYGUARD_POLLS) {
                delay(KEYGUARD_POLL_MILLIS)
                if (!power.isInteractive) return@launch
                if (!keyguard.isKeyguardLocked) { reconcile(); return@launch }
            }
        }
    }

    private fun reconcile() {
        if (!started || closed) return
        val usable = usable()
        if (mutableAttention.value != usable) {
            mutableAttention.value = usable
            if (usable) wake()
        }
        if (!usable && app.getSystemService(PowerManager::class.java).isInteractive &&
            app.getSystemService(KeyguardManager::class.java).isKeyguardLocked) watchKeyguard()
        val allowed = allowed()
        completions.setVisible(allowed)
        latest.noticeTargetIds?.let(completions::retainTargets)
        if (allowed) latest.pendingCompletion?.takeIf { latest.noticeTargetIds?.contains(it.targetId) != false }
            ?.let(completions::offer)
        val visible = allowed && CapsuleVisibility.visible(preferences.mode.value, false, true,
            latest.active || completions.current.value != null, expanded, unread = latest.badgeCount > 0)
        if (!visible) hide(clearCompletions = false)
        else if (root == null) show()
        else { updateFocusability(); place() }
    }

    private fun show() {
        val lifetime = WindowOwner()
        owner = lifetime
        val frame = CapsuleWindow(windowContext,
            header = { px(CapsuleTokens.orb) },
            drag = ::drag,
            dock = ::dock,
            collapse = { changeExpanded(false) },
            focus = { windowFocused = it },
            visibility = { visible -> if (!visible) mutableVisible.value = false },
            interaction = interaction,
        )
        root = frame
        frame.addOnAttachStateChangeListener(object : View.OnAttachStateChangeListener {
            override fun onViewAttachedToWindow(view: View) {
                if (view === root) mutableVisible.value = false
            }
            override fun onViewDetachedFromWindow(view: View) {
                if (view === root) {
                    mutableVisible.value = false
                    windowFocused = false
                }
            }
        })
        frame.setViewTreeLifecycleOwner(lifetime)
        frame.setViewTreeSavedStateRegistryOwner(lifetime)
        frame.setViewTreeViewModelStoreOwner(lifetime)
        val compose = ComposeView(windowContext).apply {
            setViewCompositionStrategy(ViewCompositionStrategy.DisposeOnViewTreeLifecycleDestroyed)
            setContent {
                val currentStyle by style.style.collectAsState()
                PersonaTheme(style = currentStyle) { theme {
                    androidx.compose.foundation.layout.Box(androidx.compose.ui.Modifier
                        .then(androidx.compose.ui.Modifier.heightIn(max = maxHeight))) {
                        val completion by completions.current.collectAsState()
                        if (!expanded && completion != null) {
                            CapsuleCompletionNotice(completion!!) { clicked ->
                                val target = completions.take(clicked.id)
                                if (target != null) { source.openCompletion(target); changeExpanded(true) }
                            }
                        } else CapsuleSurface(latest, expanded,
                            onExpand = { changeExpanded(true) },
                            onCollapse = { changeExpanded(false) },
                            onClose = { changeExpanded(false); turnOff() },
                            onSelect = source::select,
                            onSelectSection = source::selectSection,
                            onOpen = { id -> source.open(id); changeExpanded(false) },
                            onSend = { id, text -> source.send(id, text) },
                            onAction = { id, action -> source.perform(id, action) },
                            onViewed = source::viewed,
                            onDraftChange = source::updateDraft,
                            windowFocused = windowFocused,
                            drafts = drafts,
                            extraContent = extraContent)
                    }
                } }
            }
        }
        composition = compose
        frame.addView(compose, FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.WRAP_CONTENT))
        frame.addOnLayoutChangeListener { _, _, _, _, _, _, _, _, _ -> place() }
        frame.setOnApplyWindowInsetsListener { _, insets ->
            updateIme(frame, insets)
            insets
        }
        frame.viewTreeObserver.addOnGlobalLayoutListener {
            frame.rootWindowInsets?.let { updateIme(frame, it) }
        }
        frame.viewTreeObserver.addOnDrawListener {
            val notice = completions.current.value
            if (frame === root && frame.windowVisibility == View.VISIBLE && frame.isShown &&
                (!mutableVisible.value || !expanded && notice != null && announcedNotice != notice.id)) {
                frame.post {
                    if (frame === root && frame.isAttachedToWindow && frame.windowVisibility == View.VISIBLE &&
                        frame.isShown && allowed()) {
                        mutableVisible.value = true
                        if (!expanded && notice != null && completions.current.value?.id == notice.id && announcedNotice != notice.id) {
                            announcedNotice = notice.id
                            completions.noticeShown(notice.id)
                            source.noticeShown(notice.id)
                        }
                    }
                }
            }
        }
        params = WindowManager.LayoutParams(
            width(), WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            baseFlags() or WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.LEFT
            softInputMode = WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE
            setTitle(app.getString(R.string.capsule_window_title))
        }
        place(updateWindow = false)
        try {
            windows.addView(frame, params)
            registered = true
            lastLayout = params?.let { listOf(it.x, it.y, it.width, it.flags) }
            lifetime.registry.currentState = Lifecycle.State.RESUMED
        } catch (_: SecurityException) {
            hide()
        } catch (_: WindowManager.BadTokenException) {
            hide()
        }
    }

    private fun width(): Int = px(when {
        expanded -> CapsuleTokens.cardWidth
        completions.current.value != null -> CapsuleTokens.completionWidth
        else -> CapsuleTokens.compactWidth
    }).coerceAtMost(area().right - area().left)

    private fun changeExpanded(value: Boolean) {
        if (expanded == value) return
        expanded = value
        if (value) wake()
        completions.setManual(value)
        source.shown(value)
        imeTop = null
        updateFocusability()
        place()
        if (!value) reconcile()
    }

    private fun updateFocusability() {
        val view = root ?: return
        val layout = params ?: return
        val focusable = expanded
        if (expanded && (latest.listing || !latest.canSend)) {
            windowContext.getSystemService(InputMethodManager::class.java)
                .hideSoftInputFromWindow(view.windowToken, 0)
        }
        val flags = baseFlags() or if (focusable) 0 else WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE
        if (layout.flags == flags) return
        if (!focusable) releaseInput()
        layout.flags = flags
        view.isFocusableInTouchMode = focusable
        if (focusable) view.requestFocus()
    }

    private fun releaseInput() {
        root?.let { view ->
            windowContext.getSystemService(InputMethodManager::class.java).hideSoftInputFromWindow(view.windowToken, 0)
            view.clearFocus()
        }
        windowFocused = false
    }

    private fun baseFlags() = WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL or
        WindowManager.LayoutParams.FLAG_WATCH_OUTSIDE_TOUCH or WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or
        WindowManager.LayoutParams.FLAG_HARDWARE_ACCELERATED

    private fun updateIme(frame: View, insets: WindowInsets) {
        if (frame !== root) return
        val metrics = windows.currentWindowMetrics
        val localBottom = insets.getInsets(WindowInsets.Type.ime()).bottom
        val fullBottom = metrics.windowInsets.getInsets(WindowInsets.Type.ime()).bottom
        val visible = insets.isVisible(WindowInsets.Type.ime()) || metrics.windowInsets.isVisible(WindowInsets.Type.ime())
        val location = IntArray(2)
        frame.getLocationOnScreen(location)
        val top = CapsuleIme.top(metrics.bounds.height(), fullBottom, location[1], frame.height,
            localBottom, visible, imeTop)
        if (imeTop != top) {
            imeTop = top
            if (app.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0) {
                Log.d("PersonaCapsuleIme", "screen=${metrics.bounds.height()} windowY=${location[1]} height=${frame.height} local=$localBottom full=$fullBottom visible=$visible top=$top")
            }
            place()
        }
    }

    private fun area(): CapsuleArea {
        val metrics = windows.currentWindowMetrics
        val bars = metrics.windowInsets.getInsetsIgnoringVisibility(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
        val edge = px(CapsuleTokens.edge)
        return CapsuleArea(bars.left + edge, bars.top + edge,
            metrics.bounds.width() - bars.right - edge,
            (minOf(metrics.bounds.height() - bars.bottom, imeTop ?: metrics.bounds.height()) - edge)
                .coerceAtLeast(bars.top + edge))
    }

    private fun place(updateWindow: Boolean = true) {
        if (dragging) return
        val current = root ?: return
        val layout = params ?: return
        val usable = area()
        maxHeight = androidx.compose.ui.unit.Dp((usable.bottom - usable.top).coerceAtLeast(1) / windowContext.resources.displayMetrics.density)
            .coerceAtMost(CapsuleTokens.cardMaxHeight)
        layout.width = width()
        val (x, anchorY) = usable.position(position, layout.width, px(CapsuleTokens.orb))
        val y = anchorY.coerceAtMost(maxOf(usable.top, usable.bottom - current.height))
        layout.x = x
        layout.y = y
        val snapshot = listOf(layout.x, layout.y, layout.width, layout.flags)
        if (updateWindow && current.isAttachedToWindow && snapshot != lastLayout) {
            lastLayout = snapshot
            // Flags may change even when the geometry does not.
            update(current, layout)
        }
    }

    private fun drag(dx: Float, dy: Float) {
        dragging = true
        val view = root ?: return
        val layout = params ?: return
        val usable = area()
        layout.x = (layout.x + dx.roundToInt()).coerceIn(usable.left, maxOf(usable.left, usable.right - layout.width))
        layout.y = (layout.y + dy.roundToInt()).coerceIn(usable.top, maxOf(usable.top, usable.bottom - view.height))
        lastLayout = listOf(layout.x, layout.y, layout.width, layout.flags)
        update(view, layout)
    }

    private fun update(view: View, layout: WindowManager.LayoutParams) {
        try { windows.updateViewLayout(view, layout) }
        catch (_: SecurityException) { hide() }
        catch (_: WindowManager.BadTokenException) { hide() }
    }

    private fun dock() {
        dragging = false
        val layout = params ?: return
        position = area().dock(layout.x, layout.y, layout.width, px(CapsuleTokens.orb))
        preferences.dock = position
        place()
    }

    private fun hide(clearCompletions: Boolean = true) {
        if (clearCompletions) completions.setVisible(false)
        mutableVisible.value = false
        releaseInput()
        val view = root
        val lifetime = owner
        val contentView = composition
        val removeWindow = registered
        root = null
        owner = null
        composition = null
        registered = false
        announcedNotice = null
        dragging = false
        // addView registers synchronously but attaches at the next traversal. Remove the registered root
        // before destroying its owner, including when isAttachedToWindow is still false.
        if (removeWindow && view != null) {
            try { windows.removeViewImmediate(view) }
            catch (_: IllegalArgumentException) { /* The system has already removed the window. */ }
        }
        contentView?.disposeComposition()
        lifetime?.registry?.currentState = Lifecycle.State.DESTROYED
        lifetime?.viewModelStore?.clear()
        if (expanded) { expanded = false; source.shown(false) }
        completions.setManual(false)
        params = null
        lastLayout = null
        imeTop = null
    }

    override fun close() {
        if (closed) return
        closed = true
        keyguardWatch?.cancel()
        mutableAttention.value = false
        completions.close()
        hide()
        drafts.clear()
        scope.cancel()
        if (started) {
            app.unregisterReceiver(configurationChanged)
            appOps.stopWatchingMode(permissionChanged)
        }
    }

    private fun px(value: androidx.compose.ui.unit.Dp) = (value.value * windowContext.resources.displayMetrics.density).roundToInt()
}

private class WindowOwner : LifecycleOwner, SavedStateRegistryOwner, ViewModelStoreOwner {
    val registry = LifecycleRegistry(this)
    private val saved = SavedStateRegistryController.create(this)
    override val lifecycle get() = registry
    override val savedStateRegistry get() = saved.savedStateRegistry
    override val viewModelStore = ViewModelStore()
    init { saved.performAttach(); saved.performRestore(null); registry.currentState = Lifecycle.State.CREATED }
}

internal class CapsuleWindow(
    context: Context,
    private val header: () -> Int,
    private val drag: (Float, Float) -> Unit,
    private val dock: () -> Unit,
    private val collapse: () -> Unit,
    private val focus: (Boolean) -> Unit,
    private val visibility: (Boolean) -> Unit,
    private val interaction: () -> Unit = {},
) : FrameLayout(context) {
    private val slop = ViewConfiguration.get(context).scaledTouchSlop
    private var downX = 0f
    private var downY = 0f
    private var lastX = 0f
    private var lastY = 0f
    private var dragging = false
    private var headerTouch = false
    override fun onWindowFocusChanged(hasWindowFocus: Boolean) {
        super.onWindowFocusChanged(hasWindowFocus)
        focus(hasWindowFocus)
    }
    override fun onWindowVisibilityChanged(visibility: Int) {
        super.onWindowVisibilityChanged(visibility)
        this.visibility(visibility == View.VISIBLE)
    }
    override fun dispatchTouchEvent(event: MotionEvent): Boolean {
        when (event.actionMasked) {
            MotionEvent.ACTION_OUTSIDE -> { collapse(); return true }
            MotionEvent.ACTION_DOWN -> {
                interaction()
                downX = event.rawX; downY = event.rawY
                lastX = downX; lastY = downY
                headerTouch = event.y <= header()
                dragging = false
            }
            MotionEvent.ACTION_MOVE -> if (headerTouch && (dragging ||
                abs(event.rawX - downX) > slop || abs(event.rawY - downY) > slop)) {
                if (!dragging) {
                    dragging = true
                    // Compose can disallow ViewGroup interception. Own the header gesture before dispatch,
                    // and cancel its pressed button exactly once when the gesture becomes a window drag.
                    val cancel = MotionEvent.obtain(event)
                    cancel.action = MotionEvent.ACTION_CANCEL
                    super.dispatchTouchEvent(cancel)
                    cancel.recycle()
                }
                drag(event.rawX - lastX, event.rawY - lastY)
                lastX = event.rawX; lastY = event.rawY
                return true
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> if (dragging) {
                dragging = false
                dock()
                return true
            }
        }
        val handled = super.dispatchTouchEvent(event)
        return handled || headerTouch
    }
    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.keyCode == KeyEvent.KEYCODE_BACK && event.action == KeyEvent.ACTION_UP) { collapse(); return true }
        return super.dispatchKeyEvent(event)
    }
}

private const val KEYGUARD_POLL_MILLIS = 500L
/** Two minutes of a lit, locked screen; the next screen-on starts over. */
private const val KEYGUARD_POLLS = 240
