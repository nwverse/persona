package persona.feature.chat

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.After
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.AgentStartResult
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.FeedbackAction
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.hub.HubError
import persona.core.hub.HubFailure

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-rUS-w400dp-h800dp-xhdpi")
class CodexTakeoverCoordinatorTest {
    @get:Rule val rule = createComposeRule()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
    private val context = ApplicationProvider.getApplicationContext<android.content.Context>()

    @After fun tearDown() = scope.cancel()

    private fun codexSession(canResume: Boolean = true, running: Boolean = true) = Session(
        machineId = "m1",
        sessionId = "codex-1",
        title = "Codex CLI",
        cwd = "/work",
        status = if (running) "working" else "offline",
        agent = "codex",
        sourceKind = "codex_history",
        readOnlyReason = if (running) "Codex is running in Terminal" else "Exited",
        capabilities = SessionCapabilities(
            read = true,
            send = false,
            takeover = CodexTakeoverCapability(running = running, canResume = canResume),
        ),
        todo = SessionTodo(id = "todo-1", title = "Ship"),
    )

    private fun plan(running: Boolean = true, canResume: Boolean = true, reason: String? = null, token: String = "tok-1") =
        CodexTakeoverInfo(
            running = running,
            canResume = canResume,
            reason = reason,
            token = token,
            cwd = "/work",
            transcriptPath = "/tmp/rollout.jsonl",
            continuationPrompt = "Continue from transcript",
        )

    @Test fun exitedSessionTakesOverDirectlyWithoutDialogAndShowsToast() {
        val calls = mutableListOf<String>()
        val adopted = mutableListOf<Session>()
        val toasts = mutableListOf<String>()
        val source = codexSession(canResume = true, running = false)
        val live = source.copy(sourceKind = "codex_app_server", capabilities = SessionCapabilities(read = true, send = true))
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                calls += "GET"
                return plan(running = false, canResume = true)
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session {
                calls += "POST:$token:$confirmStop"
                return live
            }
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult = error("unused")
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult = error("unused")
        }
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, next -> adopted += next },
            onFailure = { msg, _, _ -> fail("Unexpected failure: $msg") },
            onSuccessToast = { toasts += it },
        )

        coordinator.takeOver(source)
        assertEquals(listOf("GET", "POST:tok-1:false"), calls)
        assertNull(coordinator.pending)
        assertFalse(coordinator.inFlight)
        assertEquals(listOf(live), adopted)
        assertEquals(listOf("Taken over"), toasts)
    }

    @Test fun runningSessionContinueInsteadUsesDedicatedProgressAndContinuationToast() {
        val calls = mutableListOf<String>()
        val adopted = mutableListOf<Session>()
        val toasts = mutableListOf<String>()
        val gate = CompletableDeferred<AgentStartResult>()
        val source = codexSession(canResume = true, running = true)
        val newSession = Session(machineId = "m1", sessionId = "new-1", status = "working", agent = "codex")
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                calls += "GET"
                return plan(running = true, canResume = true)
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session = error("takeover must not be called")
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult {
                calls += "START:${info.cwd}:${session.todo?.id}:$operationId"
                return gate.await()
            }
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult = error("unused")
        }
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, next -> adopted += next },
            onFailure = { msg, _, _ -> fail("Unexpected failure: $msg") },
            onSuccessToast = { toasts += it },
            newOperationId = { "11111111-1111-4111-8111-111111111111" },
        )

        coordinator.takeOver(source)
        assertEquals(listOf("GET"), calls)
        assertTrue(coordinator.pending is CodexTakeoverCoordinator.Pending.Takeover)

        coordinator.continueFromPending()
        assertTrue(coordinator.confirming)
        assertTrue("Continue button owns loading", coordinator.confirmingContinue)
        assertFalse("Destructive Take over button must not spin during continue-instead", coordinator.confirmingTakeover)
        coordinator.dismiss()
        assertNotNull("Dialog stays open while continue-instead is in flight", coordinator.pending)

        gate.complete(AgentStartResult.Created(newSession))
        assertEquals(listOf("GET", "START:/work:todo-1:11111111-1111-4111-8111-111111111111"), calls)
        assertNull(coordinator.pending)
        assertFalse(coordinator.confirming)
        assertEquals(listOf(newSession), adopted)
        assertEquals(listOf("Started new session"), toasts)
    }

    @Test fun unconfirmedContinuationReconcilesByOperationReceiptWithoutPostingDuplicateStart() {
        val calls = mutableListOf<String>()
        val adopted = mutableListOf<Session>()
        val failures = mutableListOf<Pair<String, String>>()
        var checkAction: (() -> Unit)? = null
        var nextOp = 0
        val source = codexSession(canResume = false, running = false)
        val created = Session(machineId = "m1", sessionId = "reconciled-9", status = "working", agent = "codex")
        val receiptStore = InMemoryContinuationReceiptStore()
        var opChecks = 0
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                calls += "GET"
                return plan(running = false, canResume = true)
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session = error("unused")
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult {
                calls += "START:$operationId"
                return AgentStartResult.Unknown
            }
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult {
                opChecks++
                calls += "OPS:$operationId"
                return if (opChecks == 1) AgentStartResult.Pending else AgentStartResult.Created(created)
            }
        }
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, next -> adopted += next },
            onFailure = { msg, label, action ->
                failures += msg to label
                checkAction = action
            },
            onSuccessToast = {},
            receiptStore = receiptStore,
            newOperationId = {
                nextOp++
                "00000000-0000-4000-8000-00000000000$nextOp"
            },
        )

        coordinator.continueInNewSession(source)
        assertEquals("00000000-0000-4000-8000-000000000001", kotlinx.coroutines.runBlocking { receiptStore.get("", source.key) })
        assertEquals("Check status", failures.last().second)

        // Calling continueInNewSession again reconciles the stored operation_id instead of posting a second start.
        coordinator.continueInNewSession(source)
        assertEquals("00000000-0000-4000-8000-000000000001", kotlinx.coroutines.runBlocking { receiptStore.get("", source.key) })

        // Invoking the toast's "Check status" action reconciles to Created and clears the stored receipt.
        checkAction!!.invoke()
        assertNull(kotlinx.coroutines.runBlocking { receiptStore.get("", source.key) })
        assertEquals(listOf(created), adopted)
        assertEquals(
            listOf(
                "GET",
                "START:00000000-0000-4000-8000-000000000001",
                "OPS:00000000-0000-4000-8000-000000000001",
                "OPS:00000000-0000-4000-8000-000000000001",
            ),
            calls,
        )
        assertEquals(1, nextOp)
    }

    @Test fun persistentReceiptStoreSharesAcrossCoordinatorsRestartsAndIsolatesScopes() {
        val prefsName = "test_codex_receipts_${java.util.UUID.randomUUID()}"
        val calls = mutableListOf<String>()
        val adopted = mutableListOf<Session>()
        val source = codexSession(canResume = false, running = false)
        val created = Session(machineId = "m1", sessionId = "persisted-1", status = "working", agent = "codex")
        val clientScopeA = object : CodexTakeoverClient {
            override fun receiptScope(session: Session) = "hub-a/pair-1"
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                calls += "GET-A"
                return plan(running = false, canResume = true)
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session = error("unused")
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult {
                calls += "START-A:$operationId"
                return AgentStartResult.Unknown
            }
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult {
                calls += "OPS-A:$operationId"
                return AgentStartResult.Created(created)
            }
        }
        val firstStore = PersistentContinuationReceiptStore(context, prefsName, Dispatchers.Unconfined)
        val firstCoordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = clientScopeA,
            scope = scope,
            onAdopted = { _, next -> adopted += next },
            onFailure = { _, _, _ -> },
            onSuccessToast = {},
            receiptStore = firstStore,
            newOperationId = { "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" },
        )
        firstCoordinator.continueInNewSession(source)
        assertEquals(listOf("GET-A", "START-A:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), calls)

        // New store instance + new coordinator (simulating preview close, entry-point switch, or process restart)
        // reconciles the persisted operationId instead of posting a duplicate start, even with a namespaced machine id.
        val secondStore = PersistentContinuationReceiptStore(context, prefsName, Dispatchers.Unconfined)
        assertNull(kotlinx.coroutines.runBlocking { secondStore.get("hub-b/pair-2", source.key) })
        val secondCoordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = clientScopeA,
            scope = scope,
            onAdopted = { _, next -> adopted += next },
            onFailure = { msg, _, _ -> fail("Unexpected failure: $msg") },
            onSuccessToast = {},
            receiptStore = secondStore,
            newOperationId = { "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" },
        )
        secondCoordinator.continueInNewSession(source.copy(machineId = GroupKeys.machine("m1", "g12345678")))
        assertEquals(
            listOf(
                "GET-A",
                "START-A:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
                "OPS-A:aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
            ),
            calls,
        )
        assertEquals(listOf(created), adopted)
        assertNull(kotlinx.coroutines.runBlocking { secondStore.get("hub-a/pair-1", source.key) })
    }

    @Test fun rejectedContinuationReceiptClearsStoredOpAndAllowsFreshRetry() {
        val calls = mutableListOf<String>()
        var retryAction: (() -> Unit)? = null
        var nextOp = 0
        val source = codexSession(canResume = false, running = false)
        val created = Session(machineId = "m1", sessionId = "fresh-2", status = "working", agent = "codex")
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                calls += "GET"
                return plan(running = false, canResume = true)
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session = error("unused")
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult {
                calls += "START:$operationId"
                return if (nextOp == 1) AgentStartResult.Rejected(CoreText.plain("provider unavailable"))
                else AgentStartResult.Created(created)
            }
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult = error("unused")
        }
        val receiptStore = InMemoryContinuationReceiptStore()
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, _ -> },
            onFailure = { _, _, action -> retryAction = action },
            onSuccessToast = {},
            receiptStore = receiptStore,
            newOperationId = {
                nextOp++
                "00000000-0000-4000-8000-00000000000$nextOp"
            },
        )

        coordinator.continueInNewSession(source)
        assertNull(kotlinx.coroutines.runBlocking { receiptStore.get("", source.key) })
        retryAction!!.invoke()
        assertEquals(
            listOf(
                "GET",
                "START:00000000-0000-4000-8000-000000000001",
                "GET",
                "START:00000000-0000-4000-8000-000000000002",
            ),
            calls,
        )
    }

    @Test fun continueOnly409TransitionsToEncryptedContinuationWithoutRetryingToken() {
        val calls = mutableListOf<String>()
        val adopted = mutableListOf<Session>()
        val source = codexSession(canResume = true, running = false)
        val continued = Session(machineId = "m1", sessionId = "new-2", status = "working", agent = "codex")
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                calls += "GET"
                return plan(running = false, canResume = true, token = "tok-once")
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session {
                calls += "POST:$token:$confirmStop"
                throw HubFailure(HubError.Server, "continue only", status = 409, code = CODEX_TAKEOVER_CONTINUE_ONLY)
            }
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult {
                calls += "START:${info.continuationPrompt}"
                return AgentStartResult.Created(continued)
            }
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult = error("unused")
        }
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, next -> adopted += next },
            onFailure = { msg, _, _ -> fail("Unexpected failure: $msg") },
            onSuccessToast = {},
        )

        coordinator.takeOver(source)
        assertEquals(listOf("GET", "POST:tok-once:false"), calls)
        val pending = coordinator.pending as CodexTakeoverCoordinator.Pending.Continuation
        assertFalse(pending.info.canResume)
        assertEquals(CodexTakeoverCoordinator.REASON_ENCRYPTED_CONTEXT, pending.info.reason)

        coordinator.confirm()
        assertEquals(listOf("GET", "POST:tok-once:false", "START:Continue from transcript"), calls)
        assertNull(coordinator.pending)
        assertEquals(listOf(continued), adopted)
    }

    @Test fun failedPostDismissesDialogAndRetryStartsFreshGet() {
        var gets = 0
        val calls = mutableListOf<String>()
        var retryCallback: (() -> Unit)? = null
        val source = codexSession(canResume = true, running = true)
        val live = source.copy(capabilities = SessionCapabilities(read = true, send = true))
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                gets++
                calls += "GET:$gets"
                return plan(running = gets == 1, canResume = true, token = "tok-$gets")
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session {
                calls += "POST:$token:$confirmStop"
                if (token == "tok-1") throw HubFailure(HubError.Server, "temporary", status = 503)
                return live
            }
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult = error("unused")
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult = error("unused")
        }
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, _ -> },
            onFailure = { _, retryLabel, retry ->
                assertTrue(retryLabel.isNotBlank())
                retryCallback = retry
            },
            onSuccessToast = {},
        )

        coordinator.takeOver(source)
        assertTrue(coordinator.pending is CodexTakeoverCoordinator.Pending.Takeover)
        coordinator.confirm()
        assertNull("Dialog must dismiss on POST failure", coordinator.pending)
        assertNotNull("Failure must expose retry action", retryCallback)

        retryCallback!!.invoke()
        assertEquals(listOf("GET:1", "POST:tok-1:true", "GET:2", "POST:tok-2:false"), calls)
    }

    @Test fun inFlightGuardBlocksDuplicateTapsShowsProgressPillAndKeepsDialogOpenWhileConfirming() {
        val getGate = CompletableDeferred<CodexTakeoverInfo>()
        val postGate = CompletableDeferred<Session>()
        var getCount = 0
        val source = codexSession(canResume = true, running = true)
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                getCount++
                return getGate.await()
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session = postGate.await()
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult = error("unused")
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult = error("unused")
        }
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, _ -> },
            onFailure = { msg, _, _ -> fail("Unexpected failure: $msg") },
            onSuccessToast = {},
        )

        rule.setContent {
            PersonaTheme {
                CodexTakeoverBusyPill(coordinator, listOf(source))
            }
        }
        rule.onAllNodesWithTag("codex-takeover-progress").assertCountEquals(0)

        coordinator.takeOver(source)
        assertTrue(coordinator.busy)
        assertTrue(coordinator.isBusyFor(source))
        coordinator.takeOver(source)
        coordinator.continueInNewSession(source)
        assertEquals(1, getCount)

        rule.onNodeWithTag("codex-takeover-progress").assertIsDisplayed()
        rule.onNodeWithText("Taking over… · Codex CLI").assertIsDisplayed()

        getGate.complete(plan(running = true, canResume = true))
        assertFalse(coordinator.busy)
        assertTrue(coordinator.pending is CodexTakeoverCoordinator.Pending.Takeover)
        rule.onAllNodesWithTag("codex-takeover-progress").assertCountEquals(0)

        coordinator.confirm()
        assertTrue(coordinator.confirmingTakeover)
        assertFalse(coordinator.confirmingContinue)
        assertNotNull("Dialog stays open while confirm POST is in flight", coordinator.pending)
        coordinator.dismiss()
        assertNotNull("Dismiss is blocked while confirm POST is in flight", coordinator.pending)

        postGate.complete(source.copy(capabilities = SessionCapabilities(read = true, send = true)))
        assertFalse(coordinator.confirming)
        assertNull(coordinator.pending)
    }

    @Test fun chatSourceNoteShowsSingleLineAndOverflowMenuForResumableAndHidesMoreForContinueOnly() {
        val calls = mutableListOf<String>()
        val resumable = codexSession(canResume = true, running = true)
        val client = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session): CodexTakeoverInfo {
                calls += "GET:${session.sessionId}"
                return plan(running = true, canResume = true)
            }
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session = resumable
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult =
                AgentStartResult.Created(resumable)
            override suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult =
                AgentStartResult.Created(resumable)
        }
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = client,
            scope = scope,
            onAdopted = { _, _ -> },
            onFailure = { _, _, _ -> },
            onSuccessToast = {},
        )
        val feedback = object : AppFeedback {
            override fun failure(error: Throwable, operation: Boolean) = true
            override fun notice(message: String) = true
            override fun notice(message: String, action: FeedbackAction, style: FeedbackStyle) = true
        }

        rule.setContent {
            PersonaTheme {
                CompositionLocalProvider(LocalAppFeedback provides feedback) {
                    Box(Modifier.width(380.dp)) {
                        ChatSourceNote(session = resumable, takeover = coordinator)
                        CodexTakeoverDialogs(coordinator)
                    }
                }
            }
        }
        rule.onNodeWithTag("chat-takeover-primary").assertIsDisplayed().assertTextEquals("Take over")
        rule.onNodeWithTag("chat-takeover-more").assertIsDisplayed().performClick()
        rule.onNodeWithText("Continue in a new session").assertIsDisplayed()

        rule.onNodeWithTag("chat-takeover-primary").performClick()
        rule.onNodeWithText("Take over this session?").assertIsDisplayed()
        rule.onNodeWithText("This will end the turn currently running in the terminal.").assertIsDisplayed()
        rule.onNodeWithText("Continue in a new session instead").assertIsDisplayed()
    }

    @Test fun chatSourceNoteContinueOnlyHidesOverflowAndStacksCleanlyOnNarrowWidth() {
        val continueOnly = codexSession(canResume = false, running = false)
        val coordinator = CodexTakeoverCoordinator(
            res = context.resources,
            client = null,
            scope = scope,
            onAdopted = { _, _ -> },
            onFailure = { _, _, _ -> },
            onSuccessToast = {},
        )
        rule.setContent {
            PersonaTheme {
                Box(Modifier.width(200.dp)) {
                    ChatSourceNote(session = continueOnly, takeover = coordinator)
                }
            }
        }
        rule.onNodeWithTag("chat-takeover-primary").assertIsDisplayed().assertTextEquals("New session")
        rule.onAllNodesWithTag("chat-takeover-more").assertCountEquals(0)
    }
}
