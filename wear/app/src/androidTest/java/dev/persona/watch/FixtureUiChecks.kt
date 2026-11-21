package dev.persona.watch

import android.app.Activity
import android.app.Instrumentation
import android.content.Intent
import android.content.res.Configuration
import android.graphics.Bitmap
import android.os.Bundle
import android.os.SystemClock
import android.view.WindowManager
import android.view.accessibility.AccessibilityNodeInfo
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.DraftScope
import dev.persona.watch.data.UrlConnectionTransport
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.security.MessageDigest

/** Synthetic mode only. User actions use visible controls; optional fault injection uses the loopback fixture. */
class FixtureUiChecks(private val instrumentation: Instrumentation) {
    private val context get() = instrumentation.targetContext
    private val automation get() = instrumentation.uiAutomation
    private lateinit var config: HubConfig
    private lateinit var client: HubClient
    private lateinit var isolation: FixtureIsolation
    private var activity: Activity? = null
    private var deadline = 0L
    private var currentStep = "guard"
    private val measurements = Bundle()

    /** Default: seven core checks. Optional regressions and nonvoice reset the synthetic server. */
    fun run(scenario: String? = null): Bundle {
        check(BuildConfig.DEBUG) { "fixture_requires_debug" }
        config = checkNotNull(CredentialStore(context).load()) { "fixture_not_paired" }.validated()
        check(config.token == FIXTURE_TOKEN) { "fixture_credential_required" }
        isolation = FixtureIsolation(config, FIXTURE_TOKEN)
        isolation.verify(config, HubDirectory(context).additional())
        client = HubClient(config, "en")
        val started = SystemClock.elapsedRealtime()
        val names = if (scenario.isNullOrBlank() || scenario == "all") SCENARIOS else scenario.split(',').map { it.trim() }
        check(names.isNotEmpty() && names.all { it in SCENARIOS || it in REGRESSIONS || it in setOf("nonvoice", "matrix") }) { "fixture_unknown_scenario" }
        deadline = started + when {
            "matrix" in names -> MATRIX_TIMEOUT_MS
            "nonvoice" in names -> NONVOICE_TIMEOUT_MS
            names.all { it in REGRESSIONS } -> REGRESSION_TIMEOUT_MS
            else -> TOTAL_TIMEOUT_MS
        }
        val passed = mutableListOf<String>()
        try {
            verifyFixture()
            for (name in names) {
                currentStep = name
                status("START", name)
                verifyFixture()
                when (name) {
                    "question" -> question()
                    "plan" -> plan()
                    "permission" -> permission()
                    "screen" -> screen()
                    "protected" -> protectedPermission()
                    "chat" -> chat()
                    "inbox" -> inbox()
                    "recovery" -> recovery()
                    "cancel_stale" -> cancelStale()
                    "nonvoice" -> nonvoice()
                    "form_accessibility" -> formAccessibility()
                    "matrix" -> matrix()
                    "credential_loss" -> measurements.putAll(CredentialLossChecks(instrumentation).run())
                }
                sameFixtureCredential()
                passed += name
                status("PASS", name)
            }
            return Bundle().apply {
                putAll(measurements)
                putBoolean("fixture", true)
                putString("fixture_passed", passed.joinToString(","))
                putInt("fixture_checks", passed.size)
                putLong("fixture_elapsed_ms", SystemClock.elapsedRealtime() - started)
                putFloat("fixture_font_scale", context.resources.configuration.fontScale)
                putInt("fixture_ui_mode_night", context.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK)
                putString("stream", "PASS: fixture UI controls (${passed.joinToString(", ")})\n")
            }
        } catch (failure: Throwable) {
            runCatching { failureScreenshot("$currentStep-failed") }
            status("FAIL", currentStep)
            throw AssertionError("fixture_ui_$currentStep", failure)
        } finally {
            activity?.let { current -> instrumentation.runOnMainSync {
                if (!current.isDestroyed) current.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            } }
        }
    }

    private fun verifyFixture() {
        sameFixtureCredential()
        val response = UrlConnectionTransport().request(config, "GET", "/api/v1/health", null, emptyMap())
        check(response.status == 200 && JSONObject(response.body).optBoolean("fixture")) { "fixture_health_required" }
        val snapshot = client.refresh()
        check(snapshot.machines.isNotEmpty() && snapshot.machines.all { it.id == "fixture" }) { "fixture_machine_required" }
        check(snapshot.sessions.isNotEmpty() && snapshot.sessions.all { it.machineId == "fixture" }) { "fixture_sessions_required" }
    }

    private fun formAccessibility(capture: String? = null) {
        open("settings")
        waitUntil("pairing_form") { nodes().count { it.isEditable } == 2 }
        InputAccessibilityChecks.verifyOnMain(instrumentation, checkNotNull(activity), 2)
        capture?.let { matrixScreenshots("$it-settings") }
        sameFixtureCredential()
        val current = instrumentation.startActivitySync(Intent(context, ConnectionsActivity::class.java).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        })
        activity = current
        waitText(text(R.string.hub_add))
        capture?.let { matrixScreenshots("$it-connections") }
        click(text(R.string.hub_add))
        InputAccessibilityChecks.verifyOnMain(instrumentation, current, 3)
        capture?.let { matrixScreenshots("$it-add-connection") }
        // No pairing token is entered or exchanged; this scenario checks the native form structure only.
        click(text(R.string.cancel))
    }

    /** Reusable at each supported system appearance/font size; all data and writes remain synthetic. */
    private fun matrix() {
        fixtureControl(JSONObject().put("reset", true).put("scenario", "matrix").put("clearEvents", true))
        fun timed(name: String, action: () -> Unit) {
            val started = SystemClock.elapsedRealtime()
            action()
            measurements.putLong("matrix_${name}_ms", SystemClock.elapsedRealtime() - started)
        }
        measurements.putString("matrix_timings_scope", "synthetic_production_ui")
        timed("home_ready") { open("home"); waitText("Sample trip planning") }
        matrixScreenshots("matrix-home")
        timed("agents_ready") { open("agents"); waitText("Sample trip planning") }
        matrixScreenshots("matrix-agents")
        timed("inbox_ready") { open("inbox"); waitText("Sample invitation") }
        matrixScreenshots("matrix-inbox")
        timed("inbox_detail") { click("Sample invitation"); waitText("A synthetic invitation for watch testing.") }
        matrixScreenshots("matrix-inbox-detail")
        setReply("")
        timed("inbox_suggestion") { click(text(R.string.inbox_ai_reply)); waitText(text(R.string.inbox_draft_ready)); waitReply(SAMPLE_AI_DRAFT) }
        matrixScreenshots("matrix-inbox-suggestion-unsent")
        check(fixtureEvents().none { it.optString("action") in setOf("inbox_reply", "inbox_done") }) { "matrix_suggestion_auto_sent" }
        timed("chat_ready") { open("session", "assistant"); waitText("Synthetic test conversation. You can safely send a sample reply here.") }
        val reply = "Sample matrix reply ${SystemClock.uptimeMillis()}"
        setReply(reply)
        matrixScreenshots("matrix-chat-composer")
        restoringFixtureOnline {
            // Delay only this isolated fixture to make the production Sending state observable.
            fixtureControl(JSONObject().put("delayMs", 5000))
            val sendStarted = SystemClock.elapsedRealtime()
            timed("send_status") { click(text(R.string.send)); waitText(text(R.string.sending)) }
            screenshot("matrix-chat-sending")
            fixtureControl(JSONObject().put("delayMs", 0))
            waitNetwork("matrix_message_receipt") {
                val session = client.refresh().sessions.single { it.sessionId == "assistant" }
                client.messages(session).count { it.own && it.text == reply } == 1
            }
            measurements.putLong("matrix_send_receipt_ms", SystemClock.elapsedRealtime() - sendStarted)
        }
        open("session", "assistant")
        waitText("Sample reply received. No real agent was started.")
        matrixScreenshots("matrix-chat-reply")
        protectedPermission()
        protectedPermission("fixture-sensitive-handoff", "matrix-protected-handoff-no-allow")
        formAccessibility("matrix")
        open("home")
        waitText("Sample trip planning")
        restoringFixtureOnline {
            fixtureControl(JSONObject().put("mode", "error"))
            click(text(R.string.refresh))
            waitText(text(R.string.connection_failed), contains = true)
            matrixScreenshots("matrix-error")
            fixtureControl(JSONObject().put("mode", "online"))
            timed("recovery_ready") { click(text(R.string.refresh)); waitText(text(R.string.connected)) }
            matrixScreenshots("matrix-recovered")
        }
        measurements.putBoolean("matrix_passed", true)
    }

    /** Save overlapping real viewports, including both ends; never stitch or redraw production pixels. */
    private fun matrixScreenshots(name: String) = screenshot(name)

    private fun sameFixtureCredential(checkDeadline: Boolean = true) {
        val current = CredentialStore(context).load()
        isolation.verify(current, HubDirectory(context).additional())
        if (checkDeadline) check(SystemClock.elapsedRealtime() < deadline) { "fixture_total_timeout" }
    }

    private fun question() {
        requirePending("fixture-question")
        open("interaction", "fixture-question")
        click("Seaside")
        screenshot("question-selected")
        click(text(R.string.submit))
        awaitResolved("fixture-question")
    }

    private fun plan() {
        requirePending("fixture-plan")
        open("interaction", "fixture-plan")
        click(text(R.string.approve))
        waitText(text(R.string.approve_body))
        screenshot("plan-confirmation")
        click(text(R.string.approve))
        awaitResolved("fixture-plan")
    }

    private fun permission() {
        requirePending("fixture-permission")
        open("interaction", "fixture-permission")
        click(text(R.string.allow))
        waitText(text(R.string.allow_body), contains = true)
        screenshot("permission-confirmation")
        click(text(R.string.allow))
        awaitResolved("fixture-permission")
    }

    private fun screen() {
        requirePending("fixture-screen")
        open("interaction", "fixture-screen")
        click("Concise")
        screenshot("screen-selected")
        click(text(R.string.submit))
        awaitResolved("fixture-screen")
    }

    private fun protectedPermission(id: String = "fixture-sensitive", capture: String = "protected-no-allow") {
        val card = requirePending(id)
        check(card.requiresInput) { "fixture_sensitive_contract" }
        open("interaction", id)
        waitText(text(R.string.requires_phone))
        check(nodes().none { matches(it, text(R.string.allow)) && clickable(it) != null }) { "fixture_sensitive_has_allow" }
        if (currentStep == "matrix") matrixScreenshots(capture) else screenshot(capture)
        click(text(R.string.deny))
        awaitResolved(id)
    }

    private fun chat() {
        val before = client.refresh().sessions.firstOrNull { it.sessionId == "working" }
        check(before?.state == "working") { "fixture_working_session_required" }
        open("session", "working")
        // The fixture marks a sent message unread, so exercise Stop while it is still working.
        click(text(R.string.stop))
        waitText(text(R.string.stop_body))
        screenshot("chat-stop-confirmation")
        click(text(R.string.stop))
        waitNetwork("stop_ack") { client.refresh().sessions.firstOrNull { it.sessionId == "working" }?.state == "idle" }
        open("session", "working")
        val message = "Sample reply from Samsung Watch UI ${SystemClock.uptimeMillis()}"
        setReply(message)
        screenshot("chat-compose")
        click(text(R.string.send))
        waitNetwork("message_ack") {
            val session = client.refresh().sessions.first { it.sessionId == "working" }
            client.messages(session).any { it.own && it.text == message }
        }
        // The refreshed transcript is visible on re-entry, using the same production transport.
        open("session", "working")
        waitText("Sample reply received. No real agent was started.")
        screenshot("chat-reply")
    }

    private fun inbox() {
        check(client.refresh().inbox.any { it.id == "fixture-inbox" && it.canReply }) { "fixture_inbox_required" }
        open("inbox")
        click("Sample invitation")
        setReply("Sample inbox reply from Samsung Watch UI ${SystemClock.uptimeMillis()}")
        screenshot("inbox-compose")
        click(text(R.string.send))
        // The sample server records the reply in its audit and leaves the item open for the Done check.
        waitText(text(R.string.saved))
        check(client.refresh().inbox.any { it.id == "fixture-inbox" }) { "fixture_reply_item_missing" }
        click(text(R.string.mark_done))
        waitText(text(R.string.mark_done_body))
        screenshot("inbox-done-confirmation")
        click(text(R.string.mark_done))
        waitNetwork("inbox_done") { client.refresh().inbox.none { it.id == "fixture-inbox" } }
    }

    private fun recovery() {
        fixtureControl(JSONObject().put("reset", true))
        val session = client.refresh().sessions.first { it.sessionId == "assistant" }
        val cached = client.messages(session).last { !it.own }.text
        val draft = "Sample recovery draft ${SystemClock.uptimeMillis()}"
        open("session", "assistant")
        waitText(cached)
        setReply(draft)
        restoringFixtureOnline {
            for (mode in listOf("error", "offline")) {
                val beforeFault = fixtureEvents().count { isFault(it, mode) }
                fixtureControl(JSONObject().put("mode", mode))
                click(text(R.string.refresh))
                waitText(text(R.string.connection_failed), contains = true)
                screenshot("recovery-$mode-error")
                check(fixtureEvents().count { isFault(it, mode) } > beforeFault) { "fixture_fault_not_observed" }
                waitText(cached)
                // Failed hub snapshots are read-only in the multi-hub client. The persisted draft
                // must survive while its composer is hidden, then reappear after reconnection.
                waitText(text(R.string.read_only))
                val draftKey = "draft:${DraftScope.hash(config.baseUrl, emptySet(), "session:${session.key}")}"
                check(context.getSharedPreferences("watch_drafts", Activity.MODE_PRIVATE).getString(draftKey, null) == draft) {
                    "fixture_offline_draft_lost"
                }
                check(nodes().none { matches(it, text(R.string.send)) && it.isEnabled && clickable(it) != null }) {
                    "fixture_offline_send_enabled"
                }
                screenshot("recovery-$mode-readonly")
                fixtureControl(JSONObject().put("mode", "online"))
                click(text(R.string.refresh))
                waitText(text(R.string.connected))
                waitReply(draft)
            }
            check(client.messages(session).none { it.own && it.text == draft }) { "fixture_draft_sent_early" }
            click(text(R.string.send))
            waitNetwork("recovery_message_ack") { client.messages(session).count { it.own && it.text == draft } == 1 }
            waitUntil("draft_cleared") { nodes().any { it.isEditable && (it.isShowingHintText || it.text.isNullOrEmpty()) } }
            val digest = MessageDigest.getInstance("SHA-256").digest(JSONObject.quote(draft).toByteArray(Charsets.UTF_8))
                .joinToString("") { "%02x".format(it) }
            check(fixtureEvents().count { it.optString("action") == "message" && it.optString("textDigest") == digest } == 1) {
                "fixture_recovery_duplicate_message"
            }
            open("session", "assistant")
            waitText("Sample reply received. No real agent was started.")
            screenshot("recovery-sent-once")
        }
    }

    private fun cancelStale() {
        fixtureControl(JSONObject().put("reset", true))
        val plan = requirePending("fixture-plan")
        val resolvePath = "/api/v1/interactions/fixture-plan/resolve"
        val before = fixtureEvents().count { it.optString("path") == resolvePath }
        val beforeGone = fixtureEvents().count { it.optString("path") == resolvePath && it.optInt("status") == 410 }
        open("interaction", "fixture-plan")
        click(text(R.string.approve))
        waitText(text(R.string.approve_body))
        click(text(R.string.cancel))
        requirePending("fixture-plan")
        check(fixtureEvents().count { it.optString("path") == resolvePath } == before) { "fixture_cancel_sent_resolution" }
        waitText(text(R.string.approve))
        screenshot("cancel-returned-to-plan")
        restoringFixtureOnline {
            fixtureControl(JSONObject().put("mode", "stale"))
            click(text(R.string.approve))
            waitText(text(R.string.approve_body))
            click(text(R.string.approve))
            waitText(text(R.string.request_gone))
            screenshot("stale-already-handled")
            click(text(R.string.refresh))
            waitNetwork("stale_removed") { client.refresh().interactions.none { it.id == "fixture-plan" } }
            waitUntil("stale_card_removed") {
                val current = nodes()
                current.isNotEmpty() && current.first().packageName?.toString() == context.packageName &&
                    current.none { matches(it, plan.title) }
            }
            val audit = fixtureControl()
            check(audit.getJSONArray("resolved").let { ids -> (0 until ids.length()).none { ids.optString(it) == "fixture-plan" } }) {
                "fixture_stale_approved"
            }
            check(fixtureEvents().count { it.optString("path") == resolvePath && it.optInt("status") == 410 } == beforeGone + 1) {
                "fixture_stale_response_missing"
            }
        }
    }

    private fun nonvoice() {
        fixtureControl(JSONObject().put("reset", true).put("scenario", "nonvoice").put("clearEvents", true))
        verifyFixture()

        // Use the normal list row: the debug session route alone does not establish mark-seen behavior.
        val unread = client.refresh().sessions.single { it.sessionId == "unread" }
        check(unread.state == "unread") { "fixture_unread_required" }
        open("agents")
        click(unread.title)
        waitNetwork("seen_ack") {
            client.refresh().sessions.single { it.sessionId == "unread" }.state == "idle" &&
                fixtureEvents().any { it.optString("action") == "seen" && it.optString("session") == "unread" }
        }
        val unsentDraft = "Unsent sample draft ${SystemClock.uptimeMillis()}"
        setReply(unsentDraft)
        click(text(R.string.continue_action))
        waitNetwork("continue_ack") {
            client.messages(unread).count { it.own && it.text == text(R.string.continue_action) } == 1
        }
        waitReply(unsentDraft)
        check(client.messages(unread).none { it.own && it.text == unsentDraft }) { "fixture_continue_sent_draft" }
        check(fixtureEvents().count { it.optString("action") == "message" && it.optString("session") == "unread" } == 1) {
            "fixture_continue_duplicate"
        }
        screenshot("nonvoice-continue-keeps-draft")
        status("PASS", "nonvoice_continue_seen")

        // Cancel must preserve the existing draft and must not even ask the server to generate another.
        open("inbox")
        click("Sample invitation")
        // The unchanged empty composer accepts generation directly into its persisted draft.
        setReply("")
        click(text(R.string.inbox_ai_reply))
        waitText(text(R.string.inbox_draft_ready))
        waitReply(SAMPLE_AI_DRAFT)
        val inboxItem = client.refresh().inbox.single { it.id == "fixture-inbox" }
        val generatedHash = DraftScope.hash(config.baseUrl, emptySet(), "inbox:${inboxItem.manager.orEmpty()}:${inboxItem.id}")
        val savedDrafts = context.getSharedPreferences("watch_drafts", Activity.MODE_PRIVATE)
        check(savedDrafts.getString("draft:$generatedHash", null) == SAMPLE_AI_DRAFT &&
            !savedDrafts.getString("revision:$generatedHash", null).isNullOrBlank()) { "fixture_empty_ai_draft_not_stored" }
        open("inbox")
        click("Sample invitation")
        waitReply(SAMPLE_AI_DRAFT)
        check(fixtureEvents().count { it.optString("action") == "inbox_draft" } == 1 &&
            fixtureEvents().none { it.optString("action") in setOf("inbox_reply", "inbox_done") }) { "fixture_empty_ai_draft_not_retained" }
        status("PASS", "nonvoice_empty_ai_draft")
        fixtureControl(JSONObject().put("clearEvents", true))
        val originalDraft = "Original sample inbox draft ${SystemClock.uptimeMillis()}"
        setReply(originalDraft)
        click(text(R.string.inbox_ai_reply))
        waitText(text(R.string.inbox_replace_draft_body))
        screenshot("nonvoice-replace-draft-confirmation")
        click(text(R.string.cancel))
        waitReply(originalDraft)
        check(fixtureEvents().none { it.optString("action") in setOf("inbox_draft", "inbox_reply", "inbox_done") }) {
            "fixture_cancel_generated_or_sent"
        }
        click(text(R.string.inbox_ai_reply))
        waitText(text(R.string.inbox_replace_draft_body))
        click(text(R.string.inbox_ai_reply))
        waitText(text(R.string.inbox_draft_ready))
        waitReply(SAMPLE_AI_DRAFT)
        val drafts = fixtureEvents().filter { it.optString("action") == "inbox_draft" }
        check(drafts.size == 1 && drafts.single().optInt("instructionLength", -1) == 0 && drafts.single().optBoolean("managerMatched")) {
            "fixture_draft_contract"
        }
        check(fixtureEvents().none { it.optString("action") in setOf("inbox_reply", "inbox_done") }) { "fixture_draft_auto_sent" }
        check(client.refresh().inbox.any { it.id == "fixture-inbox" }) { "fixture_draft_removed_item" }
        screenshot("nonvoice-ai-draft-unsent")
        status("PASS", "nonvoice_ai_draft")

        open("interaction", "fixture-plan")
        click(text(R.string.handle_elsewhere))
        awaitResolved("fixture-plan")
        val dismiss = fixtureEvents().single { it.optString("action") == "resolve" && it.optString("id") == "fixture-plan" }
        check(dismiss.optBoolean("dismiss") && dismiss.getJSONArray("fields").let { it.length() == 1 && it.optString(0) == "dismiss" }) {
            "fixture_handoff_approved"
        }
        screenshot("nonvoice-handoff-complete")
        status("PASS", "nonvoice_handoff")

        open("interaction", "fixture-screen")
        click(text(R.string.screen_escape))
        awaitResolved("fixture-screen")
        checkResolutionKey("fixture-screen", "esc")
        open("interaction", "fixture-screen-enter")
        click(text(R.string.screen_enter))
        waitText(text(R.string.screen_enter_body))
        screenshot("nonvoice-enter-confirmation")
        click(text(R.string.cancel))
        waitText(text(R.string.screen_enter))
        requirePending("fixture-screen-enter")
        check(fixtureEvents().none { it.optString("path") == "/api/v1/interactions/fixture-screen-enter/resolve" }) {
            "fixture_enter_cancel_sent"
        }
        click(text(R.string.screen_enter))
        waitText(text(R.string.screen_enter_body))
        click(text(R.string.screen_enter))
        awaitResolved("fixture-screen-enter")
        checkResolutionKey("fixture-screen-enter", "enter")
        status("PASS", "nonvoice_screen_keys")

        val readOnly = requirePending("fixture-screen-readonly")
        check(!readOnly.canAnswer) { "fixture_readonly_contract" }
        open("interaction", readOnly.id)
        waitText(text(R.string.requires_phone))
        waitText(text(R.string.screen_escape))
        val controls = nodes()
        check(controls.isNotEmpty()) { "fixture_readonly_window_missing" }
        for (label in listOf(text(R.string.screen_escape), text(R.string.screen_enter))) {
            check(controls.any { matches(it, label) && !it.isEnabled }) { "fixture_readonly_keys_enabled" }
        }
        check(controls.none { matches(it, text(R.string.handle_elsewhere)) && clickable(it) != null }) { "fixture_readonly_handoff_enabled" }
        check(fixtureEvents().none { it.optString("path") == "/api/v1/interactions/fixture-screen-readonly/resolve" }) {
            "fixture_readonly_resolved"
        }
        screenshot("nonvoice-readonly-controls")
        status("PASS", "nonvoice_readonly")
    }

    private fun checkResolutionKey(id: String, key: String) {
        val events = fixtureEvents().filter { it.optString("action") == "resolve" && it.optString("id") == id }
        check(events.size == 1 && events.single().getJSONArray("keys").let { it.length() == 1 && it.optString(0) == key }) {
            "fixture_screen_key_contract"
        }
    }

    private fun restoringFixtureOnline(block: () -> Unit) {
        var primaryFailure: Throwable? = null
        try {
            block()
        } catch (failure: Throwable) {
            primaryFailure = failure
            throw failure
        } finally {
            try {
                fixtureControl(JSONObject().put("mode", "online").put("delayMs", 0), cleanup = true)
            } catch (cleanupFailure: Throwable) {
                val original = primaryFailure
                if (original != null) original.addSuppressed(cleanupFailure) else throw cleanupFailure
            }
        }
    }

    private fun waitReply(value: String) = waitUntil("draft_retained") {
        val field = nodes().firstOrNull { it.isEditable && it.text?.toString() == value }
        if (field?.isVisibleToUser == true) true else { revealOrScroll { it.isEditable }; false }
    }

    private fun isFault(event: JSONObject, mode: String) =
        event.optString("path").startsWith("/api/v1/") &&
            if (mode == "offline") event.optString("action") == "connection_closed" else event.optInt("status") == 503

    private fun fixtureEvents(): List<JSONObject> {
        val events = fixtureControl().getJSONArray("events")
        return (0 until events.length()).map { events.getJSONObject(it) }
    }

    /** Test controls are never sent to LAN hosts or the real hub, even with an accidentally reused token. */
    private fun fixtureControl(body: JSONObject? = null, cleanup: Boolean = false): JSONObject {
        sameFixtureCredential(checkDeadline = !cleanup)
        val origin = FixtureControlOrigin.parse(config.baseUrl)
        fun request(path: String, payload: JSONObject?): JSONObject {
            val connection = origin.url(path).openConnection() as HttpURLConnection
            try {
                connection.instanceFollowRedirects = false
                connection.connectTimeout = 2_000
                connection.readTimeout = 3_000
                connection.requestMethod = if (payload == null) "GET" else "POST"
                connection.setRequestProperty("Authorization", "Bearer $FIXTURE_TOKEN")
                connection.setRequestProperty("Accept", "application/json")
                if (payload != null) {
                    connection.doOutput = true
                    connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
                    val bytes = payload.toString().toByteArray(Charsets.UTF_8)
                    connection.setFixedLengthStreamingMode(bytes.size)
                    connection.outputStream.use { it.write(bytes) }
                }
                check(connection.responseCode == 200) { "fixture_control_failed" }
                val result = JSONObject(connection.inputStream.bufferedReader(Charsets.UTF_8).use { it.readText() })
                check(result.optBoolean("fixture")) { "fixture_control_marker_required" }
                return result
            } finally {
                connection.disconnect()
            }
        }
        request("/api/v1/health", null)
        return request("/__test", body)
    }

    private fun requirePending(id: String) = checkNotNull(client.refresh().interactions.firstOrNull { it.id == id }) {
        "fixture_pending_required"
    }

    private fun awaitResolved(id: String) = waitNetwork("resolved") {
        val snapshot = client.refresh()
        snapshot.interactions.none { it.id == id } && snapshot.sessions.firstOrNull {
            it.sessionId == id.removePrefix("fixture-")
        }?.state == "idle"
    }

    private fun open(screen: String, id: String? = null) {
        sameFixtureCredential()
        activity?.let { old -> instrumentation.runOnMainSync {
            if (!old.isDestroyed) old.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        } }
        val intent = Intent(context, MainActivity::class.java).apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
            putExtra("screen", screen)
            if (id != null) putExtra(if (screen == "session") "session_id" else "interaction_id", id)
        }
        val current = instrumentation.startActivitySync(intent)
        activity = current
        instrumentation.runOnMainSync { current.window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) }
        waitUntil("app_window") { automation.rootInActiveWindow?.packageName?.toString() == context.packageName }
    }

    private fun click(label: String) {
        sameFixtureCredential()
        waitUntil("click") {
            val found = nodes().firstOrNull { matches(it, label) && it.isVisibleToUser && it.isEnabled && clickable(it) != null }
            if (found != null && clickable(found)?.performAction(AccessibilityNodeInfo.ACTION_CLICK) == true) {
                SystemClock.sleep(ACTION_SETTLE_MS)
                true
            } else {
                revealOrScroll { matches(it, label) }
                false
            }
        }
    }

    private fun setReply(value: String) {
        sameFixtureCredential()
        waitUntil("reply_field") {
            val field = nodes().firstOrNull { it.isEditable && it.isEnabled && it.isVisibleToUser }
            if (field != null) {
                val args = Bundle().apply { putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, value) }
                field.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)
            } else {
                revealOrScroll { it.isEditable }
                false
            }
        }
        waitUntil("reply_value") { nodes().any { it.isEditable &&
            if (value.isEmpty()) it.isShowingHintText || it.text.isNullOrEmpty() else it.text?.toString() == value } }
        SystemClock.sleep(ACTION_SETTLE_MS)
    }

    private fun waitText(label: String, contains: Boolean = false) = waitUntil("text") {
        val found = nodes().firstOrNull { matches(it, label, contains) && it.isVisibleToUser }
        if (found != null) true else { revealOrScroll { matches(it, label, contains) }; false }
    }

    private fun revealOrScroll(match: (AccessibilityNodeInfo) -> Boolean) {
        val current = nodes()
        val hidden = current.firstOrNull(match)
        if (hidden?.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_SHOW_ON_SCREEN.id) == true) return
        current.firstOrNull { it.isScrollable && it.isVisibleToUser }
            ?.performAction(AccessibilityNodeInfo.ACTION_SCROLL_FORWARD)
    }

    private fun clickable(node: AccessibilityNodeInfo): AccessibilityNodeInfo? {
        var current: AccessibilityNodeInfo? = node
        repeat(8) {
            val value = current ?: return null
            if (value.isClickable && value.isEnabled && value.packageName?.toString() == context.packageName) return value
            current = value.parent
        }
        return null
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

    private fun matches(node: AccessibilityNodeInfo, label: String, contains: Boolean = false): Boolean =
        listOf(node.text?.toString(), node.contentDescription?.toString()).any { value ->
            value != null && if (contains) value.contains(label) else value == label
        }

    private fun waitUntil(reason: String, condition: () -> Boolean) {
        val end = minOf(deadline, SystemClock.elapsedRealtime() + STEP_TIMEOUT_MS)
        while (SystemClock.elapsedRealtime() < end) {
            sameFixtureCredential()
            val complete = condition()
            sameFixtureCredential()
            if (complete) return
            SystemClock.sleep(POLL_MS)
        }
        throw AssertionError("fixture_${currentStep}_${reason}_timeout")
    }

    private fun waitNetwork(reason: String, condition: () -> Boolean) = waitUntil(reason) {
        sameFixtureCredential()
        condition()
    }

    private fun screenshot(name: String) {
        ViewportCapture(instrumentation, currentActivity = { checkNotNull(activity) },
            check = { sameFixtureCredential() }, save = ::saveScreenshot).capture(name)
    }

    private fun saveScreenshot(name: String, bitmap: Bitmap) {
        val folder = File(context.filesDir, "fixture-qa").apply { mkdirs() }
        File(folder, "$name.png").outputStream().use {
            check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) { "fixture_screenshot_write" }
        }
    }

    /** Preserve diagnostics even after a deadline failure; never capture another app's window. */
    private fun failureScreenshot(name: String) {
        sameFixtureCredential(checkDeadline = false)
        check(automation.rootInActiveWindow?.packageName?.toString() == context.packageName) { "fixture_screenshot_window" }
        val bitmap = checkNotNull(automation.takeScreenshot()) { "fixture_screenshot_failed" }
        try {
            sameFixtureCredential(checkDeadline = false)
            check(automation.rootInActiveWindow?.packageName?.toString() == context.packageName) { "fixture_screenshot_window" }
            saveScreenshot(name, bitmap)
        } finally { bitmap.recycle() }
    }

    private fun text(id: Int) = context.getString(id)
    private fun status(verdict: String, step: String) = instrumentation.sendStatus(0, Bundle().apply {
        putString("stream", "$verdict: fixture UI $step\n")
        putString("fixture_step", step)
    })

    companion object {
        private const val FIXTURE_TOKEN = "persona-wear-fixture-only"
        private val SCENARIOS = listOf("question", "plan", "permission", "screen", "protected", "chat", "inbox")
        private val REGRESSIONS = listOf("recovery", "cancel_stale", "form_accessibility", "credential_loss")
        private const val MATRIX_TIMEOUT_MS = 180_000L
        private const val TOTAL_TIMEOUT_MS = 95_000L
        private const val REGRESSION_TIMEOUT_MS = 60_000L
        private const val NONVOICE_TIMEOUT_MS = 120_000L
        private const val SAMPLE_AI_DRAFT = "Thank you for the sample invitation. I can join the synthetic review."
        private const val STEP_TIMEOUT_MS = 12_000L
        private const val POLL_MS = 200L
        private const val ACTION_SETTLE_MS = 250L
    }
}
