package dev.persona.watch

import android.app.Activity
import android.app.Instrumentation
import android.content.Intent
import android.graphics.Bitmap
import android.os.Bundle
import android.os.SystemClock
import android.view.WindowManager
import android.view.accessibility.AccessibilityNodeInfo
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.WatchMessage
import dev.persona.watch.data.WatchSession
import java.io.File
import java.net.URI
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** Explicitly selected, pre-created acceptance chat only. Writes go through the visible Send button. */
class LivePingChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext
    private val automation get() = instrumentation.uiAutomation
    private lateinit var config: HubConfig
    private lateinit var client: HubClient
    private lateinit var isolation: LiveWriteIsolation
    private lateinit var transcript: LivePingTranscript
    private lateinit var session: WatchSession
    private var activity: Activity? = null
    private var deadline = 0L

    fun run(sessionId: String): Bundle {
        check(BuildConfig.DEBUG) { "live_ping_requires_debug" }
        check(sessionId.isNotBlank() && sessionId == sessionId.trim()) { "live_ping_explicit_session_required" }
        config = checkNotNull(CredentialStore(context).load()) { "live_ping_not_paired" }.validated()
        check(config.token != FIXTURE_TOKEN) { "live_ping_real_credential_required" }
        check(URI(config.baseUrl).scheme == "https" && config.fingerprint != null) { "live_ping_pinned_tls_required" }
        isolation = LiveWriteIsolation(config, FIXTURE_TOKEN)
        client = HubClient(config, "en")
        val started = SystemClock.elapsedRealtime()
        deadline = started + TOTAL_TIMEOUT_MS
        sameCredential()
        session = checkNotNull(client.refresh().sessions.singleOrNull { it.sessionId == sessionId }) {
            "live_ping_exact_unique_session_required"
        }
        sameCredential()
        check(session.groupId.isEmpty()) { "live_ping_primary_session_required" }
        check(session.machineId != "fixture") { "live_ping_real_session_required" }
        val initial = client.messages(session)
        sameCredential()
        transcript = LivePingTranscript(initial, INITIAL_MARKER, READY, PING, PONG)
        val exchange = LivePingExchange(initial, PING, PONG, SystemClock.elapsedRealtime())
        if (exchange.shouldSend) check(session.canSend) { "live_ping_session_read_only" }
        var clickedSend = false
        var sendStarted = SystemClock.elapsedRealtime()
        try {
            status("guarded")
            openSession()
            if (exchange.shouldSend) {
                // Observe the same acceptance-chat marker in the production UI before editing its composer.
                waitText(READY)
                setReply()
                clickedSend = clickSendOnce(exchange) { sendStarted = SystemClock.elapsedRealtime() }
                if (clickedSend) status("sent_via_ui")
            }
            val reader = Executors.newSingleThreadExecutor()
            val finalMessages = try {
                exchange.awaitReply(deadline, now = SystemClock::elapsedRealtime, verify = ::sameCredential,
                    read = { remaining ->
                        val read = reader.submit<List<WatchMessage>> { client.messages(session) }
                        val messages = try { read.get(remaining, TimeUnit.MILLISECONDS) }
                        catch (_: Exception) {
                            read.cancel(true)
                            throw AssertionError("live_ping_reply_read_failed")
                        }
                        verifyDedicatedChat(messages)
                        messages
                    }, wait = SystemClock::sleep)
            } finally {
                reader.shutdownNow()
            }
            // Re-enter through the production route, then actually reveal the returned assistant text.
            openSession()
            waitText(PONG)
            screenshot()
            status("pong_visible")
            return Bundle().apply {
                putBoolean("live_ping", true)
                putBoolean("live_ping_sent_by_ui", clickedSend)
                putBoolean("live_ping_reused_existing_ping", !clickedSend)
                putInt("live_ping_own_ping_count", finalMessages.count { it.own && it.text == PING })
                putInt("live_ping_pong_count", finalMessages.count { isAssistant(it, PONG) })
                putInt("live_ping_transcript_messages", finalMessages.size)
                putLong("live_ping_ack_ms", if (clickedSend) checkNotNull(exchange.acknowledgedAt) - sendStarted else 0)
                putLong("live_ping_roundtrip_ms", if (clickedSend) checkNotNull(exchange.completedAt) - sendStarted else 0)
                putLong("live_ping_elapsed_ms", SystemClock.elapsedRealtime() - started)
                putString("live_ping_screenshot", "files/fixture-qa/real-pong.png")
                putString("stream", "PASS: real watch UI to TLS hub to agent; PONG visible\n")
            }
        } finally {
            clearKeepScreenOn()
        }
    }

    private fun verifyDedicatedChat(messages: List<WatchMessage>) {
        sameCredential()
        transcript.verify(messages)
    }

    private fun isAssistant(message: WatchMessage, value: String) =
        !message.own && message.role == "assistant" && message.text.trim() == value

    private fun sameCredential() {
        isolation.verify(CredentialStore(context).load(), HubDirectory(context).additional())
        check(SystemClock.elapsedRealtime() < deadline) { "live_ping_total_timeout" }
    }

    private fun openSession() {
        sameCredential()
        clearKeepScreenOn()
        val current = instrumentation.startActivitySync(Intent(context, MainActivity::class.java).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
            putExtra("screen", "session")
            putExtra("session_id", session.sessionId)
        })
        activity = current
        instrumentation.runOnMainSync { current.window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) }
        waitUntil("app_window") { automation.rootInActiveWindow?.packageName?.toString() == context.packageName }
        waitText(session.title)
    }

    private fun setReply() {
        waitUntil("reply_field") {
            val field = nodes().firstOrNull { it.isEditable && it.isEnabled && it.isVisibleToUser }
            if (field == null) {
                revealOrScroll { it.isEditable }
                false
            } else {
                // Do not overwrite an unrelated draft, even inside the dedicated test conversation.
                check(field.isShowingHintText || field.text.isNullOrEmpty() || field.text?.toString() == PING) { "live_ping_unrelated_draft" }
                sameCredential()
                field.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, Bundle().apply {
                    putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, PING)
                })
            }
        }
        waitUntil("reply_value") { nodes().any { it.isEditable && it.text?.toString() == PING } }
    }

    private fun clickSendOnce(exchange: LivePingExchange, sending: () -> Unit): Boolean {
        val label = context.getString(R.string.send)
        var button: AccessibilityNodeInfo? = null
        waitUntil("send_visible") {
            val found = nodes().firstOrNull { matches(it, label) && it.isVisibleToUser && it.isEnabled && it.isClickable }
            if (found == null) { revealOrScroll { matches(it, label) }; false }
            else { button = found; true }
        }
        sameCredential()
        // The UI wait can advance the bounded history window. Recheck its stable anchor immediately before sending.
        val beforeClick = client.messages(session)
        verifyDedicatedChat(beforeClick)
        exchange.observe(beforeClick, SystemClock.elapsedRealtime())
        return exchange.sendIfMissing {
            sameCredential()
            sending()
            // Exactly one attempt. A false/uncertain UI result must never trigger another click automatically.
            check(checkNotNull(button).performAction(AccessibilityNodeInfo.ACTION_CLICK)) { "live_ping_send_click_unconfirmed" }
        }
    }

    private fun waitText(value: String) = waitUntil("text_visible") {
        if (nodes().any { matches(it, value) && it.isVisibleToUser }) true
        else { revealOrScroll { matches(it, value) }; false }
    }

    private fun revealOrScroll(match: (AccessibilityNodeInfo) -> Boolean) {
        val current = nodes()
        val hidden = current.firstOrNull(match)
        if (hidden?.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_SHOW_ON_SCREEN.id) == true) return
        current.firstOrNull { it.isScrollable && it.isVisibleToUser }?.performAction(AccessibilityNodeInfo.ACTION_SCROLL_FORWARD)
    }

    private fun nodes(): List<AccessibilityNodeInfo> {
        val root = automation.rootInActiveWindow ?: return emptyList()
        if (root.packageName?.toString() != context.packageName) return emptyList()
        val result = mutableListOf<AccessibilityNodeInfo>()
        fun visit(node: AccessibilityNodeInfo, depth: Int) {
            if (depth > 30 || result.size >= 1000) return
            result += node
            for (index in 0 until node.childCount) node.getChild(index)?.let { visit(it, depth + 1) }
        }
        visit(root, 0)
        return result
    }

    private fun matches(node: AccessibilityNodeInfo, value: String) =
        node.text?.toString() == value || node.contentDescription?.toString() == value

    private fun waitUntil(reason: String, condition: () -> Boolean) {
        val end = minOf(deadline, SystemClock.elapsedRealtime() + UI_TIMEOUT_MS)
        while (SystemClock.elapsedRealtime() < end) {
            sameCredential()
            if (condition()) { sameCredential(); return }
            SystemClock.sleep(UI_POLL_MS)
        }
        throw AssertionError("live_ping_${reason}_timeout")
    }

    private fun screenshot() {
        sameCredential()
        check(nodes().any { matches(it, PONG) && it.isVisibleToUser }) { "live_ping_pong_not_visible" }
        val bitmap = checkNotNull(automation.takeScreenshot()) { "live_ping_screenshot_failed" }
        try {
            val folder = File(context.filesDir, "fixture-qa").apply { mkdirs() }
            File(folder, "real-pong.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        } finally { bitmap.recycle() }
    }

    private fun clearKeepScreenOn() {
        activity?.let { current -> instrumentation.runOnMainSync {
            if (!current.isDestroyed) current.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        } }
    }

    private fun status(step: String) = instrumentation.sendStatus(0, Bundle().apply {
        putString("live_ping_step", step)
        putString("stream", "Live watch ping: $step\n")
    })

    companion object {
        private const val FIXTURE_TOKEN = "persona-wear-fixture-only"
        private const val INITIAL_MARKER = "Samsung Watch Persona"
        private const val READY = "WATCH_READY"
        private const val PING = "PING_WATCH"
        private const val PONG = "PONG_WATCH"
        private const val TOTAL_TIMEOUT_MS = 110_000L
        private const val UI_TIMEOUT_MS = 15_000L
        private const val UI_POLL_MS = 200L
    }
}
