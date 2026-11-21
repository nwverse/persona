package persona.android.assist

import android.app.assist.AssistContent
import android.app.assist.AssistStructure
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.drawable.ColorDrawable
import android.net.Uri
import android.os.Bundle
import android.service.voice.VoiceInteractionSession
import android.util.Log
import android.view.View
import android.view.ViewGroup
import android.view.Window
import android.view.WindowManager
import android.view.inputmethod.InputMethodManager
import android.widget.FrameLayout
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.platform.ComposeView
import androidx.compose.ui.platform.ViewCompositionStrategy
import androidx.core.view.WindowCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.withContext
import persona.android.MainActivity
import persona.android.R
import persona.core.contracts.SessionKey
import persona.core.designsystem.AppLanguages
import persona.core.navigation.AskPersonaLink
import persona.core.navigation.NotificationRoute
import persona.feature.chat.AssistDraftBus
import persona.feature.chat.ChatLinkContext

/**
 * Hosts Persona's Gemini/Muse-style floating quick overlay directly above the foreground app when
 * triggered as Android's Default Digital Assistant (`ROLE_ASSISTANT`).
 *
 * - Keeps the session alive (`setUiEnabled(true)`) so async `onHandleScreenshot` and `onHandleAssist`
 *   callbacks reliably arrive even when the OS passes gesture flags without `SHOW_WITH_SCREENSHOT`.
 * - Stages any received screenshot and filtered safe context strictly in memory; disk persistence
 *   and attachment occur only upon explicit user tap on "Attach current screen".
 * - Sends directly to the connected Hub main conversation and streams Persona's reply in place.
 */
class PersonaVoiceInteractionSession(context: Context) : VoiceInteractionSession(context) {
    private val uiContext: Context by lazy { AppLanguages.wrap(context) }
    private val sessionScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val lifecycleOwner = AssistSessionLifecycleOwner(fallbackOnBackPressed = ::dismissOverlay)

    private lateinit var bridge: AssistHubBridge
    private lateinit var controller: AssistOverlayController
    private var rootFrame: FrameLayout? = null
    private var composeView: ComposeView? = null

    override fun onCreate() {
        super.onCreate()
        lifecycleOwner.onCreate()
        bridge = AssistHubBridge(
            context = uiContext,
            scope = sessionScope,
            onSnapshot = { snapshot -> controller.onHubUpdate(snapshot) },
        )
        controller = createOverlayController(
            uiContext = uiContext,
            scope = sessionScope,
            bridge = bridge,
            launchFullApp = { mainSession -> launchMainActivity(mainSession) },
            dismiss = ::dismissOverlay,
        )
    }

    override fun onCreateContentView(): View {
        configureOverlayWindow(window?.window)
        val frame = FrameLayout(uiContext).apply {
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
        }
        lifecycleOwner.attachTo(window?.window?.decorView, frame)
        val compose = ComposeView(uiContext).apply {
            setViewCompositionStrategy(ViewCompositionStrategy.DisposeOnViewTreeLifecycleDestroyed)
            setContent {
                val ui by controller.state.collectAsState()
                AssistOverlayEnvironment(onOpenExternalUri = ::openExternalUri) {
                    AssistOverlaySheet(
                        ui = ui,
                        onDraftChange = controller::updateDraftText,
                        onDictationHeard = controller::appendDictation,
                        onNotice = controller::showNotice,
                        onAttachScreen = controller::attachCurrentScreen,
                        onRemoveScreen = controller::removeAttachedScreen,
                        onSend = controller::send,
                        onOpenInPersona = controller::openInPersona,
                        onDismiss = ::dismissOverlay,
                    )
                }
            }
        }
        frame.addView(
            compose,
            FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            ),
        )
        rootFrame = frame
        composeView = compose
        return frame
    }

    override fun onPrepareShow(args: Bundle?, showFlags: Int) {
        super.onPrepareShow(args, showFlags)
        setUiEnabled(true)
        Log.i(ASSIST_LOG_TAG, "onPrepareShow showFlags=$showFlags hasArgs=${args != null}")
        if (::bridge.isInitialized && ::controller.isInitialized) {
            bridge.start()
            controller.beginSession(supportsScreenshot = true, showFlags = showFlags)
        }
    }

    override fun onShow(args: Bundle?, showFlags: Int) {
        super.onShow(args, showFlags)
        Log.i(ASSIST_LOG_TAG, "onShow showFlags=$showFlags hasArgs=${args != null}")
        configureOverlayWindow(window?.window)
        rootFrame?.let { frame -> lifecycleOwner.attachTo(window?.window?.decorView, frame) }
        lifecycleOwner.onShow()
        if (::bridge.isInitialized && ::controller.isInitialized) {
            bridge.start()
            controller.beginSession(supportsScreenshot = true, showFlags = showFlags)
        }
    }

    override fun onHandleAssist(state: AssistState) {
        if (!::controller.isInitialized) return
        val contextCard = buildSafeContextCard(state.assistData, state.assistStructure, state.assistContent)
        Log.i(
            ASSIST_LOG_TAG,
            "onHandleAssist(state) hasStructure=${state.assistStructure != null} " +
                "hasContent=${state.assistContent != null} focused=${state.isFocused} safeContext=${contextCard != null}",
        )
        controller.onAssistContextReceived(contextCard, isFocused = state.isFocused)
    }

    @Deprecated("Deprecated in Java")
    @Suppress("DEPRECATION")
    override fun onHandleAssist(data: Bundle?, structure: AssistStructure?, content: AssistContent?) {
        if (!::controller.isInitialized) return
        val contextCard = buildSafeContextCard(data, structure, content)
        Log.i(
            ASSIST_LOG_TAG,
            "onHandleAssist(legacy) hasStructure=${structure != null} " +
                "hasContent=${content != null} safeContext=${contextCard != null}",
        )
        controller.onAssistContextReceived(contextCard, isFocused = true)
    }

    override fun onHandleScreenshot(screenshot: Bitmap?) {
        if (!::controller.isInitialized) return
        controller.onScreenshotReceived(screenshot)
    }

    override fun onComputeInsets(outInsets: Insets) {
        super.onComputeInsets(outInsets)
        // Full-frame touchable region so tapping the backdrop scrim dismisses cleanly while keeping
        // the underlying app unresized and system navigation gestures intact.
        outInsets.touchableInsets = Insets.TOUCHABLE_INSETS_FRAME
    }

    override fun onBackPressed() {
        lifecycleOwner.dispatchBackPressed()
    }

    override fun onHide() {
        Log.i(ASSIST_LOG_TAG, "onHide")
        hideKeyboard()
        if (::controller.isInitialized) controller.endSession()
        if (::bridge.isInitialized) bridge.stop()
        lifecycleOwner.onHide()
        super.onHide()
    }

    override fun onDestroy() {
        Log.i(ASSIST_LOG_TAG, "onDestroy")
        hideKeyboard()
        if (::controller.isInitialized) controller.endSession()
        if (::bridge.isInitialized) bridge.stop()
        composeView?.disposeComposition()
        composeView = null
        rootFrame = null
        lifecycleOwner.onDestroy()
        sessionScope.cancel()
        super.onDestroy()
    }

    private fun dismissOverlay() {
        hideKeyboard()
        if (::controller.isInitialized) controller.endSession()
        if (::bridge.isInitialized) bridge.stop()
        runCatching { hide() }
        runCatching { finish() }
    }

    private fun hideKeyboard() {
        val token = rootFrame?.windowToken ?: window?.window?.decorView?.windowToken ?: return
        runCatching {
            context.getSystemService(InputMethodManager::class.java)?.hideSoftInputFromWindow(token, 0)
        }
    }

    private fun buildSafeContextCard(
        data: Bundle?,
        structure: AssistStructure?,
        content: AssistContent?,
    ): ChatLinkContext? {
        val snapshot = AssistContextExtractor.snapshotFromAssist(data, structure, content)
        val safe = AssistContextExtractor.extract(snapshot, context.packageName)
        return AssistContextExtractor.toLinkContext(
            safe = safe,
            id = controller.state.value.invocationId.ifEmpty { "assist-context" },
            fallbackTitle = uiContext.getString(R.string.app_assist_context_title),
        )
    }

    private fun launchMainActivity(mainSession: SessionKey?) {
        val intent = assistMainIntent(context, mainSession)
        val launched = runCatching {
            startAssistantActivity(intent)
            true
        }.getOrDefault(false)
        if (!launched) {
            runCatching { context.startActivity(intent) }
        }
    }

    private fun openExternalUri(uri: String) {
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse(uri)).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        val launched = runCatching {
            startAssistantActivity(intent)
            true
        }.getOrDefault(false)
        if (!launched) {
            runCatching { context.startActivity(intent) }
        }
        dismissOverlay()
    }
}

internal fun configureOverlayWindow(window: Window?) {
    if (window == null) return
    window.setBackgroundDrawable(ColorDrawable(android.graphics.Color.TRANSPARENT))
    window.setLayout(WindowManager.LayoutParams.MATCH_PARENT, WindowManager.LayoutParams.MATCH_PARENT)
    window.clearFlags(
        WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_ALT_FOCUSABLE_IM,
    )
    window.setSoftInputMode(
        WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE or WindowManager.LayoutParams.SOFT_INPUT_STATE_HIDDEN,
    )
    WindowCompat.setDecorFitsSystemWindows(window, false)
}

internal fun createOverlayController(
    uiContext: Context,
    scope: CoroutineScope,
    bridge: AssistHubBridge,
    launchFullApp: (SessionKey?) -> Unit,
    dismiss: () -> Unit,
): AssistOverlayController {
    val appContext = uiContext.applicationContext
    return AssistOverlayController(
        mainScope = scope,
        prepareScreenshot = { bitmap ->
            withContext(Dispatchers.Default) { AssistScreenshotStore.prepareInMemory(bitmap) }
        },
        createThumbnail = { bitmap ->
            withContext(Dispatchers.Default) { AssistScreenshotStore.createPreviewThumbnail(bitmap) }
        },
        saveScreenshot = { bitmap, id ->
            withContext(Dispatchers.IO) { AssistScreenshotStore.save(appContext, bitmap, id) }
        },
        deleteScreenshot = { attachment ->
            withContext(Dispatchers.IO) { AssistScreenshotStore.deleteSaved(appContext, attachment) }
        },
        sendTextToMain = bridge::sendTextToMain,
        sendWithScreenToMain = bridge::sendWithScreenToMain,
        publishDraftForFullApp = { id, screenshot, contextCard, text ->
            AssistDraftBus.publish(id = id, screenshot = screenshot, context = contextCard, text = text)
        },
        onWakeHub = bridge::wakeHub,
        onLaunchFullApp = launchFullApp,
        onDismiss = dismiss,
        screenOnlyDisplayLabel = { uiContext.getString(R.string.app_assist_screen_only_prompt) },
        defaultSendError = { uiContext.getString(R.string.app_assist_send_failed) },
    )
}

/** Builds the launch intent that opens Persona's main chat (or specific [mainSession]) in full screen. */
internal fun assistMainIntent(context: Context, mainSession: SessionKey? = null): Intent {
    val targetUri = if (mainSession != null) {
        NotificationRoute.conversation(mainSession.machineId, mainSession.sessionId, assistant = true).uri()
    } else {
        Uri.parse(AskPersonaLink)
    }
    return Intent(Intent.ACTION_VIEW, targetUri, context, MainActivity::class.java).apply {
        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
    }
}
