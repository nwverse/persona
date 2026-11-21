package persona.android.assist

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.lifecycle.lifecycleScope
import persona.android.R
import persona.core.designsystem.AppLanguages

/**
 * Fallback entry point for launchers and OEM paths that dispatch `android.intent.action.ASSIST`
 * directly rather than binding a [android.service.voice.VoiceInteractionSession]. Renders the
 * same transparent bottom floating overlay card over the previous app.
 */
class AssistActivity : ComponentActivity() {
    override fun attachBaseContext(base: Context) = super.attachBaseContext(AppLanguages.wrap(base))

    private lateinit var bridge: AssistHubBridge
    private lateinit var controller: AssistOverlayController

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        configureOverlayWindow(window)

        bridge = AssistHubBridge(
            context = this,
            scope = lifecycleScope,
            onSnapshot = { snapshot -> controller.onHubUpdate(snapshot) },
        )
        controller = createOverlayController(
            uiContext = this,
            scope = lifecycleScope,
            bridge = bridge,
            launchFullApp = { mainSession ->
                runCatching { startActivity(assistMainIntent(this, mainSession)) }
            },
            dismiss = { finish() },
        )

        bridge.start()
        controller.beginSession(supportsScreenshot = false)
        stageSafeIntentContext(intent)

        setContent {
            val ui by controller.state.collectAsState()
            BackHandler {
                controller.endSession()
                finish()
            }
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
                    onDismiss = {
                        controller.endSession()
                        finish()
                    },
                )
            }
        }
    }

    override fun onDestroy() {
        if (::controller.isInitialized) controller.endSession()
        if (::bridge.isInitialized) bridge.stop()
        super.onDestroy()
    }

    private fun stageSafeIntentContext(source: Intent?) {
        val snapshot = AssistContextExtractor.snapshotFromIntent(source)
        val safe = AssistContextExtractor.extract(snapshot, packageName)
        val contextCard = AssistContextExtractor.toLinkContext(
            safe = safe,
            id = controller.state.value.invocationId.ifEmpty { "assist-activity" },
            fallbackTitle = getString(R.string.app_assist_context_title),
        )
        controller.onAssistContextReceived(contextCard, isFocused = true)
    }

    private fun openExternalUri(uri: String) {
        val target = Intent(Intent.ACTION_VIEW, Uri.parse(uri)).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        runCatching { startActivity(target) }
        controller.endSession()
        finish()
    }
}
