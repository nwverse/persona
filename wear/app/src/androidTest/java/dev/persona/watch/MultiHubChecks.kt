package dev.persona.watch

import android.app.Activity
import android.app.Instrumentation
import android.content.Intent
import android.graphics.Bitmap
import android.os.Bundle
import android.os.SystemClock
import android.view.KeyEvent
import android.view.WindowManager
import android.view.accessibility.AccessibilityNodeInfo
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubConfig
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/** Two independent synthetic servers only. Creates/removes one test connection through production UI. */
class MultiHubChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext
    private val automation get() = instrumentation.uiAutomation
    private val directory get() = HubDirectory(context)
    private lateinit var primary: HubConfig
    private lateinit var secondary: HubConfig
    private lateinit var primaryClient: HubClient
    private lateinit var secondaryClient: HubClient
    private var secondaryId: String? = null
    private var adding = false
    private var activity: Activity? = null
    private var deadline = 0L
    private var step = "guard"
    private var groupName = ""
    private lateinit var originalEnvelope: ByteArray

    fun run(secondaryUrl: String): Bundle {
        check(BuildConfig.DEBUG) { "multihub_requires_debug" }
        primary = checkNotNull(CredentialStore(context).load()) { "multihub_primary_required" }.validated()
        secondary = HubConfig(secondaryUrl, FIXTURE_TOKEN).validated()
        requireLoopback(primary)
        requireLoopback(secondary)
        check(primary.baseUrl != secondary.baseUrl) { "multihub_distinct_origins_required" }
        check(directory.additional().isEmpty()) { "multihub_existing_groups_untouched" }
        check(!directory.hasPendingRegistrations()) { "multihub_existing_pending_untouched" }
        val beforeIds = directory.registrationIds()
        originalEnvelope = File(context.noBackupFilesDir, "connection.enc").readBytes()
        primaryClient = HubClient(primary, "en")
        secondaryClient = HubClient(secondary, "en")
        val started = SystemClock.elapsedRealtime()
        deadline = started + TOTAL_TIMEOUT_MS
        groupName = "Sample second hub ${SystemClock.uptimeMillis()}"
        val firstInstance = request(primary, "/api/v1/health").optString("instance")
        val secondInstance = request(secondary, "/api/v1/health").optString("instance")
        check(firstInstance.isNotBlank() && secondInstance.isNotBlank() && firstInstance != secondInstance) {
            "multihub_independent_fixture_instances_required"
        }
        verifySynthetic(primaryClient)
        verifySynthetic(secondaryClient)
        val owned = ConcurrentHashMap<String, PendingHubRegistration>()
        val recorded = CountDownLatch(1)
        val aborting = AtomicBoolean(false)
        val inputKey = registrationInputKey(secondary)
        var observation: AutoCloseable? = null
        observation = directory.observeRegistrations { entry ->
            if (entry.id !in beforeIds && entry.name == groupName && entry.inputKey == inputKey &&
                HubConfig.sameOrigin(entry.baseUrl, secondary.baseUrl)) {
                owned[entry.id] = entry
                // A queued add that starts after cleanup begins must stop before consuming its token.
                try {
                    if (aborting.get()) { directory.fenceRegistration(entry); observation?.close() }
                } finally { recorded.countDown() }
            }
        }
        var primaryFailure: Throwable? = null
        try {
            control(primary, JSONObject().put("reset", true).put("clearEvents", true))
            control(secondary, JSONObject().put("reset", true).put("clearEvents", true))
            status("pairing")
            open(ConnectionsActivity::class.java)
            click(text(R.string.hub_add))
            setField(0, groupName)
            setField(1, secondary.baseUrl)
            setField(2, FIXTURE_TOKEN)
            adding = true
            click(text(R.string.connect))
            waitUntil("test_connection_saved") {
                val group = directory.additional().singleOrNull { owned.containsKey(it.id) && HubConfig.sameOrigin(it.config?.baseUrl, secondary.baseUrl) }
                if (group == null) false else {
                    check(group.config?.token == FIXTURE_TOKEN) { "multihub_wrong_test_credential" }
                    secondaryId = group.id
                    adding = false
                    true
                }
            }
            waitText(text(R.string.hub_added))
            check(control(secondary).getJSONArray("events").let { rows -> (0 until rows.length()).any {
                rows.getJSONObject(it).optString("path") == "/api/v1/auth/tokens" && rows.getJSONObject(it).optInt("status") == 200
            } }) { "multihub_pairing_not_observed" }
            val envelope = File(context.noBackupFilesDir, "group_${checkNotNull(secondaryId)}.enc").readText()
            check(!envelope.contains(FIXTURE_TOKEN) && !envelope.contains(secondary.baseUrl)) { "multihub_credential_plaintext" }
            check(File(context.noBackupFilesDir, "connection.enc").readBytes().contentEquals(originalEnvelope)) { "multihub_primary_credential_rewritten" }
            screenshot("multihub-connections")

            status("isolated_drafts")
            val firstDraft = "Sample primary draft ${SystemClock.uptimeMillis()}"
            val secondDraft = "Sample secondary draft ${SystemClock.uptimeMillis()}"
            openSession(false)
            setReply(firstDraft)
            openSession(true)
            check(!currentDraft().contains(firstDraft)) { "multihub_primary_draft_leaked" }
            setReply(secondDraft)
            openSession(false)
            waitReply(firstDraft)
            screenshot("multihub-primary-draft")
            click(text(R.string.send))
            waitMessage(primaryClient, firstDraft)
            check(!messages(secondaryClient).contains(firstDraft)) { "multihub_primary_sent_to_secondary" }
            openSession(true)
            waitReply(secondDraft)
            screenshot("multihub-secondary-draft")
            click(text(R.string.send))
            waitMessage(secondaryClient, secondDraft)
            check(!messages(primaryClient).contains(secondDraft)) { "multihub_secondary_sent_to_primary" }
            check(messages(primaryClient).count { it == firstDraft } == 1 && messages(secondaryClient).count { it == secondDraft } == 1) {
                "multihub_duplicate_message"
            }

            status("offline_secondary")
            // Keep this activity alive: it must retain the secondary's successful snapshot before the failure.
            nodes().filter { it.isEditable }.forEach { it.performAction(AccessibilityNodeInfo.ACTION_CLEAR_FOCUS) }
            control(secondary, JSONObject().put("mode", "error"))
            click(text(R.string.refresh))
            waitText(text(R.string.read_only))
            val blocked = nodes()
            check(blocked.isNotEmpty() && blocked.none {
                it.isEnabled && clickable(it) != null && (matches(it, text(R.string.send)) || matches(it, text(R.string.continue_action)) || matches(it, text(R.string.stop)))
            }) { "multihub_offline_session_writable" }
            screenshot("multihub-secondary-offline")
            status("offline_return_home")
            instrumentation.sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
            waitText(text(R.string.agents))
            status("offline_secondary_approval")
            clickRequest("Sample report review", secondaryGroup = true)
            waitText(groupName)
            waitText(text(R.string.requires_phone))
            check(nodes().none { it.isEnabled && clickable(it) != null &&
                (matches(it, text(R.string.allow)) || matches(it, text(R.string.deny)) || matches(it, text(R.string.handle_elsewhere))) }) {
                "multihub_offline_approval_enabled"
            }
            screenshot("multihub-secondary-approval-disabled")
            val afterOffline = events(secondary)
            check(afterOffline.none { it.optString("method") == "POST" && it.optString("path").startsWith("/api/v1/interactions/") }) {
                "multihub_offline_resolution_sent"
            }

            status("primary_still_online")
            openSession(false)
            val afterFault = "Sample primary online ${SystemClock.uptimeMillis()}"
            setReply(afterFault)
            click(text(R.string.send))
            waitMessage(primaryClient, afterFault)
            screenshot("multihub-primary-still-online")
            control(secondary, JSONObject().put("mode", "online"))
            check(!messages(secondaryClient).contains(afterFault)) { "multihub_offline_routing_failed" }

            status("remove_test_connection")
            open(ConnectionsActivity::class.java)
            waitText(groupName)
            click(text(R.string.hub_remove))
            waitText(text(R.string.hub_remove_body))
            click(text(R.string.hub_remove))
            waitUntil("test_connection_removed") { directory.additional().isEmpty() }
            check(CredentialStore(context).load() == primary) { "multihub_primary_changed" }
            check(File(context.noBackupFilesDir, "connection.enc").readBytes().contentEquals(originalEnvelope)) { "multihub_primary_envelope_changed" }
            check(!File(context.noBackupFilesDir, "group_${checkNotNull(secondaryId)}.enc").exists()) { "multihub_removed_credential_remains" }
            secondaryId = null
            verifySynthetic(primaryClient)
            screenshot("multihub-primary-kept")
            return Bundle().apply {
                putBoolean("multihub", true)
                putBoolean("multihub_distinct_fixture_instances", true)
                putBoolean("multihub_pairing_via_ui", true)
                putBoolean("multihub_drafts_isolated", true)
                putBoolean("multihub_messages_routed", true)
                putBoolean("multihub_offline_read_only", true)
                putBoolean("multihub_primary_preserved", true)
                putBoolean("multihub_credential_encrypted", true)
                putLong("multihub_elapsed_ms", SystemClock.elapsedRealtime() - started)
                putString("stream", "PASS: two synthetic hubs, real watch UI, isolated drafts/routes, offline controls and encrypted credentials\n")
            }
        } catch (failure: Throwable) {
            primaryFailure = failure
            runCatching { screenshot("multihub-$step-failed") }
            throw AssertionError("multihub_$step", failure)
        } finally {
            aborting.set(true)
            val teardownFailure = runCatching {
                activity?.let { current -> instrumentation.runOnMainSync {
                    if (!current.isDestroyed) {
                        current.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                        if (adding) current.finish()
                    }
                } }
            }.exceptionOrNull()
            val cleanup = runCatching {
                val worker = Executors.newSingleThreadExecutor()
                try {
                    drainFixtureRegistrations(
                        owned = { owned.values.toList() },
                        fence = { directory.fenceRegistration(it) },
                        abort = { entry, budget ->
                            val task = worker.submit { directory.abortRegistration(entry) }
                            try { task.get(budget, TimeUnit.MILLISECONDS) }
                            catch (failure: Exception) { task.cancel(true); throw failure }
                        },
                        awaitRecord = if (adding) ({ budget: Long -> recorded.await(budget, TimeUnit.MILLISECONDS) }) else null,
                        now = SystemClock::elapsedRealtime,
                    )
                } finally { worker.shutdownNow() }
            }.exceptionOrNull()
            // A late, not-yet-recorded UI add retains its observer only to fence itself and then detach.
            if (!adding || owned.isNotEmpty()) observation?.close()
            val resetErrors = listOf(primary, secondary).mapNotNull { connection ->
                runCatching { control(connection, JSONObject().put("mode", "online"), cleanup = true) }.exceptionOrNull()
            }
            val failures = listOfNotNull(cleanup, teardownFailure) + resetErrors
            val cleanupFailure = failures.firstOrNull()
            failures.drop(1).forEach { cleanupFailure?.addSuppressed(it) }
            if (cleanupFailure != null) {
                val original = primaryFailure
                if (original != null) original.addSuppressed(cleanupFailure) else throw cleanupFailure
            }
        }
    }

    private fun requireLoopback(config: HubConfig) {
        val uri = URI(config.baseUrl)
        check(config.token == FIXTURE_TOKEN && uri.scheme == "http" &&
            uri.host in setOf("127.0.0.1", "localhost", "::1", "[::1]") && uri.path.orEmpty() in setOf("", "/") &&
            uri.userInfo == null && uri.query == null && uri.fragment == null) { "multihub_loopback_fixture_required" }
    }

    private fun sameFixtures() {
        check(CredentialStore(context).load() == primary) { "multihub_primary_credential_changed" }
        val groups = directory.additional()
        check(groups.size <= 1 && groups.all { it.name == groupName && it.config?.token == FIXTURE_TOKEN && it.config?.baseUrl == secondary.baseUrl &&
            (adding || secondaryId == it.id) }) { "multihub_unexpected_connection" }
        check(SystemClock.elapsedRealtime() < deadline) { "multihub_total_timeout" }
    }

    private fun verifySynthetic(client: HubClient) {
        val snapshot = client.refresh()
        check(snapshot.machines.isNotEmpty() && snapshot.machines.all { it.id == "fixture" } &&
            snapshot.sessions.isNotEmpty() && snapshot.sessions.all { it.machineId == "fixture" }) { "multihub_fixture_data_required" }
    }

    private fun request(config: HubConfig, path: String, payload: JSONObject? = null): JSONObject {
        requireLoopback(config)
        check(path in setOf("/api/v1/health", "/__test")) { "multihub_control_path" }
        val connection = URL(config.baseUrl + path).openConnection() as HttpURLConnection
        try {
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 2_000
            connection.readTimeout = 3_000
            connection.requestMethod = if (payload == null) "GET" else "POST"
            connection.setRequestProperty("Authorization", "Bearer $FIXTURE_TOKEN")
            if (payload != null) {
                val bytes = payload.toString().toByteArray(Charsets.UTF_8)
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
                connection.setFixedLengthStreamingMode(bytes.size)
                connection.outputStream.use { it.write(bytes) }
            }
            check(connection.responseCode == 200) { "multihub_control_unavailable" }
            return JSONObject(connection.inputStream.bufferedReader(Charsets.UTF_8).use { it.readText() }).also {
                check(it.optBoolean("fixture")) { "multihub_fixture_marker_required" }
            }
        } finally { connection.disconnect() }
    }

    private fun control(config: HubConfig, payload: JSONObject? = null, cleanup: Boolean = false): JSONObject {
        if (!cleanup) sameFixtures()
        return request(config, "/__test", payload)
    }
    private fun events(config: HubConfig): List<JSONObject> = control(config).getJSONArray("events").let { rows ->
        (0 until rows.length()).map(rows::getJSONObject)
    }
    private fun messages(client: HubClient): List<String> {
        val session = client.refresh().sessions.single { it.sessionId == "working" }
        return client.messages(session).filter { it.own }.map { it.text }
    }
    private fun waitMessage(client: HubClient, value: String) = waitUntil("message_ack") { messages(client).count { it == value } == 1 }

    private fun open(type: Class<out Activity>, screen: String? = null) {
        sameFixtures()
        activity?.let { old -> instrumentation.runOnMainSync { if (!old.isDestroyed) old.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) } }
        val current = instrumentation.startActivitySync(Intent(context, type).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
            screen?.let { putExtra("screen", it) }
        })
        activity = current
        instrumentation.runOnMainSync { current.window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) }
        waitUntil("app_window") { automation.rootInActiveWindow?.packageName?.toString() == context.packageName }
    }

    private fun openSession(secondaryGroup: Boolean) {
        open(MainActivity::class.java, "agents")
        clickCard("Sample task in progress", secondaryGroup)
        waitText("Sample task in progress")
        if (secondaryGroup) waitText(groupName)
    }
    private fun clickRequest(title: String, secondaryGroup: Boolean) = clickCard(title, secondaryGroup)
    private fun clickCard(title: String, secondaryGroup: Boolean) {
        clickWhere { node ->
            val label = node.contentDescription?.toString().orEmpty()
            node.isClickable && label.startsWith("$title. ") && label.contains(groupName) == secondaryGroup
        }
    }

    private fun click(label: String) = clickWhere { matches(it, label) }
    private fun clickWhere(match: (AccessibilityNodeInfo) -> Boolean) {
        var button: AccessibilityNodeInfo? = null
        waitUntil("click_target") {
            val target = nodes().firstOrNull { match(it) && it.isEnabled && it.isVisibleToUser && clickable(it) != null }
            if (target == null) { revealOrScroll(match); false } else { button = clickable(target); true }
        }
        sameFixtures()
        check(checkNotNull(button).performAction(AccessibilityNodeInfo.ACTION_CLICK)) { "multihub_click_failed" }
        SystemClock.sleep(250)
    }

    private fun setField(index: Int, value: String) {
        waitUntil("field") {
            val node = nodes().filter { it.isEditable }.getOrNull(index)
            if (node == null || !node.isVisibleToUser) {
                node?.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_SHOW_ON_SCREEN.id)
                false
            } else node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, Bundle().apply {
                putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, value)
            })
        }
        waitUntil("field_value") {
            val field = nodes().filter { it.isEditable }.getOrNull(index)
            // Password text may be redacted by accessibility. The guarded pairing exchange verifies its value.
            field?.isPassword == true || field?.text?.toString() == value
        }
    }
    private fun setReply(value: String) { setField(0, value); waitReply(value) }
    private fun currentDraft(): String {
        waitUntil("composer") {
            if (nodes().any { it.isEditable && it.isVisibleToUser }) true else { revealOrScroll { it.isEditable }; false }
        }
        val field = nodes().first { it.isEditable && it.isVisibleToUser }
        return if (field.isShowingHintText) "" else field.text?.toString().orEmpty()
    }
    private fun waitReply(value: String) = waitUntil("draft") {
        val field = nodes().firstOrNull { it.isEditable && it.text?.toString() == value }
        if (field?.isVisibleToUser == true) true else { revealOrScroll { it.isEditable }; false }
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
            if (depth > 30 || rows.size >= 1000) return
            rows += node
            for (i in 0 until node.childCount) node.getChild(i)?.let { visit(it, depth + 1) }
        }
        visit(root, 0)
        return rows
    }
    // Android's native confirmation buttons expose their all-caps presentation through accessibility.
    private fun matches(node: AccessibilityNodeInfo, value: String) = node.text?.toString()?.equals(value, ignoreCase = true) == true || node.contentDescription?.toString() == value
    private fun waitUntil(reason: String, condition: () -> Boolean) {
        val end = minOf(deadline, SystemClock.elapsedRealtime() + STEP_TIMEOUT_MS)
        while (SystemClock.elapsedRealtime() < end) {
            sameFixtures()
            if (condition()) return
            SystemClock.sleep(200)
        }
        throw AssertionError("multihub_${step}_${reason}_timeout")
    }
    private fun screenshot(name: String) {
        ViewportCapture(instrumentation, currentActivity = { checkNotNull(activity) },
            check = { sameFixtures() }, save = { filename, bitmap ->
                val folder = File(context.filesDir, "fixture-qa").apply { mkdirs() }
                File(folder, "$filename.png").outputStream().use {
                    check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) { "multihub_screenshot_write_failed" }
                }
            }).capture(name)
    }
    private fun text(id: Int) = context.getString(id)
    private fun status(value: String) {
        step = value
        instrumentation.sendStatus(0, Bundle().apply {
            putString("multihub_step", value)
            putString("stream", "Synthetic multi-hub: $value\n")
        })
    }
    companion object {
        private const val FIXTURE_TOKEN = "persona-wear-fixture-only"
        private const val TOTAL_TIMEOUT_MS = 180_000L
        private const val STEP_TIMEOUT_MS = 20_000L
    }
}
