package persona.core.data

import persona.core.contracts.HubScope
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.Resolution
import persona.core.contracts.SessionKey
import persona.core.contracts.api.InboxAssistantConnection
import persona.core.contracts.api.InboxAssistantTransport
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MainConversationResolver
import persona.core.contracts.api.MessageEvidence
import persona.core.contracts.api.MessageRequest
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.MessageTransport
import persona.core.contracts.api.MessageTransportResult
import persona.feature.inbox.InboxHandOffRequest
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.nio.file.Files

/**
 * Delivery, not a local outbox write, gates an Agent hand-off's record. These tests use the real repository and the
 * real outbox sender together: only the two hub transports and durable storage are fakes.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class InboxHandOffDeliveryRegressionTest {
    private val pairing = HubScope("test-hub", "generation-a")
    private val agent = SessionKey("laptop", "agent")
    private val operationId = "handoff-client-id"
    private val request = InboxHandOffRequest(
        "item", "Handle this", agent.machineId, agent.sessionId, operationId, targetTitle = "Example agent",
    )

    @Test fun onlineAcceptedMessageSendsBeforeRecordAgentHandOff() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        val accepted = CompletableDeferred<MessageTransportResult>()
        fixture.messages.onSend = { accepted.await() }
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(listOf(operationId), fixture.messages.requests.map { it.clientMessageId })
            assertTrue("an in-flight message is not accepted delivery", fixture.inbox.records.isEmpty())

            accepted.complete(MessageTransportResult.Accepted)
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.Sent, fixture.store.state(operationId))
            assertEquals(
                listOf("send:$operationId", "accepted:$operationId", "record:$operationId"),
                fixture.events,
            )
            assertEquals(listOf(RecordedHandOff("item", agent, "Example agent", operationId)), fixture.inbox.records)

            // The transcript copy is independent evidence of the same delivery, not a second hand-off.
            fixture.sender.observeTranscript(agent, listOf(
                Item("echo", 1, role = "user", kind = "text", text = request.text, clientMsgId = operationId),
            ))
            runCurrent()
            assertEquals(OutboxState.Shown, fixture.store.state(operationId))
            assertEquals(1, fixture.messages.requests.size)
            assertEquals(listOf(RecordedHandOff("item", agent, "Example agent", operationId)), fixture.inbox.records)
            assertEquals(
                listOf("send:$operationId", "accepted:$operationId", "record:$operationId"),
                fixture.events,
            )
        } finally {
            accepted.complete(MessageTransportResult.Accepted)
            runCurrent()
            fixture.close()
        }
    }

    @Test fun onlineHttp409RefusalDoesNotRecord() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        // The real HTTP transport maps a 409 refusal to this result; it is not a transport exception.
        fixture.messages.answers += MessageTransportResult.HubError("input_draft", "none", retryable = false)
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.Refused, fixture.store.state(operationId))
            assertEquals(1, fixture.messages.requests.size)
            assertTrue(fixture.inbox.records.isEmpty())
        } finally {
            fixture.close()
        }
    }

    @Test fun onlineOtherHttpFailuresAndRetryingStatesDoNotRecord() = runTest {
        val cases = listOf(
            "HTTP 500 with unknown delivery" to MessageTransportResult.HubError("internal", "unknown"),
            "HTTP 503 waiting for retry" to MessageTransportResult.HubError("assistant_offline", "none", retryable = true),
            "HTTP 500 without delivery evidence" to MessageTransportResult.HubError("internal", error = "HTTP 500"),
        )
        for ((label, answer) in cases) {
            val fixture = HandOffFixture(backgroundScope, pairing, online = true)
            runCurrent() // Finish the sender's own startup drain before measuring this attempt.
            fixture.messages.answers += answer
            try {
                val handOff = async { fixture.repository.handOff(request) }
                runCurrent()
                assertEquals(label, agent, handOff.await())
                assertTrue(label, fixture.store.state(operationId) != OutboxState.Sent)
                assertEquals(label, 1, fixture.messages.requests.size)
                assertTrue(label, fixture.inbox.records.isEmpty())
            } finally {
                fixture.close()
            }
        }
    }

    @Test fun onlineHttpDeliveredYesFailureDoesNotRecord() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        fixture.messages.answers += MessageTransportResult.HubError("internal", "yes", error = "HTTP 500")
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals("the legacy outbox still shows its HTTP delivery hint", OutboxState.Sent,
                fixture.store.state(operationId))
            assertEquals(1, fixture.messages.requests.size)
            assertTrue("an HTTP error is not an Accepted result", fixture.inbox.records.isEmpty())
        } finally {
            fixture.close()
        }
    }

    @Test fun manualDeliveredConfirmationDoesNotRecord() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        fixture.messages.answers += MessageTransportResult.NoReceipt
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.Unknown, fixture.store.state(operationId))

            assertTrue(fixture.sender.confirm(operationId, delivered = true))
            runCurrent()
            assertEquals(OutboxState.Sent, fixture.store.state(operationId))
            assertTrue("the user's manual disposition is not hub evidence", fixture.inbox.records.isEmpty())
        } finally {
            fixture.close()
        }
    }

    @Test fun sentStateAloneAndGraceDrivenShownDoNotRecord() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = false)
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.WaitingForNetwork, fixture.store.state(operationId))

            // Isolate state observation from every authoritative cause: even an externally forced Sent row is insufficient.
            assertTrue(fixture.store.transition(operationId, setOf(OutboxState.WaitingForNetwork), OutboxState.Sent))
            runCurrent()
            assertTrue(fixture.inbox.records.isEmpty())
            advanceTimeBy(60_000)
            runCurrent()
            assertEquals(OutboxState.Shown, fixture.store.state(operationId))
            assertTrue("the display grace is not new delivery evidence", fixture.inbox.records.isEmpty())
            assertTrue(fixture.messages.requests.isEmpty())
        } finally {
            fixture.close()
        }
    }

    @Test fun onlineTransportFailureDoesNotRecord() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        fixture.messages.onSend = { throw IOException("connection lost") }
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.Unknown, fixture.store.state(operationId))
            assertEquals(1, fixture.messages.requests.size)
            assertTrue(fixture.inbox.records.isEmpty())
        } finally {
            fixture.close()
        }
    }

    @Test fun acceptedReceiptDuringReconciliationRecordsWithoutResending() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        fixture.messages.answers += MessageTransportResult.NoReceipt
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.Unknown, fixture.store.state(operationId))
            assertTrue(fixture.inbox.records.isEmpty())

            fixture.messages.evidence[operationId] = MessageEvidence.Receipt(MessageTransportResult.Accepted)
            fixture.messages.readyToSend.value = false
            runCurrent()
            fixture.messages.readyToSend.value = true
            runCurrent()
            assertEquals(OutboxState.Sent, fixture.store.state(operationId))
            assertEquals(1, fixture.messages.requests.size)
            assertEquals(listOf(RecordedHandOff("item", agent, "Example agent", operationId)), fixture.inbox.records)
        } finally {
            fixture.close()
        }
    }

    @Test fun validatedTranscriptEvidenceRecordsAnUncertainMessageOnce() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        fixture.messages.answers += MessageTransportResult.NoReceipt
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.Unknown, fixture.store.state(operationId))
            assertTrue(fixture.inbox.records.isEmpty())

            val other = Item("other", 1, role = "user", kind = "text", text = "Someone else's words")
            fixture.sender.observeTranscript(agent, listOf(other))
            runCurrent()
            assertEquals(OutboxState.Unknown, fixture.store.state(operationId))
            assertTrue("unmatched transcript text is not evidence", fixture.inbox.records.isEmpty())

            val echo = Item("echo", 2, role = "user", kind = "text", text = request.text, clientMsgId = operationId)
            fixture.sender.observeTranscript(agent, listOf(other, echo))
            fixture.sender.observeTranscript(agent, listOf(other, echo))
            runCurrent()
            assertEquals(OutboxState.Shown, fixture.store.state(operationId))
            assertEquals(1, fixture.messages.requests.size)
            assertEquals(listOf(RecordedHandOff("item", agent, "Example agent", operationId)), fixture.inbox.records)
        } finally {
            fixture.close()
        }
    }

    @Test fun offlineEnqueueDoesNotRecord() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = false)
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.WaitingForNetwork, fixture.store.state(operationId))
            assertTrue(fixture.messages.requests.isEmpty())
            assertTrue(fixture.inbox.records.isEmpty())
        } finally {
            fixture.close()
        }
    }

    @Test fun queuedFailureAndRefusalDoNotRecord() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = false)
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            fixture.messages.answers += MessageTransportResult.HubError(
                "assistant_offline", "none", retryable = true,
            )
            fixture.messages.readyToSend.value = true
            runCurrent()
            assertEquals(OutboxState.WaitingForNetwork, fixture.store.state(operationId))

            fixture.messages.answers += MessageTransportResult.HubError("input_draft", "none", retryable = false)
            assertTrue(fixture.sender.retry(operationId))
            runCurrent()
            assertEquals(OutboxState.Refused, fixture.store.state(operationId))
            assertEquals(2, fixture.messages.requests.size)
            assertTrue(fixture.inbox.records.isEmpty())
        } finally {
            fixture.close()
        }
    }

    @Test fun queuedLaterSuccessRecordsOnceWithStableClientId() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = false)
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            val recordsAtEnqueue = fixture.inbox.records.size
            assertTrue(fixture.messages.requests.isEmpty())

            fixture.messages.answers += MessageTransportResult.Accepted
            fixture.messages.readyToSend.value = true
            runCurrent()
            assertEquals(OutboxState.Sent, fixture.store.state(operationId))
            assertEquals(listOf(operationId), fixture.messages.requests.map { it.clientMessageId })
            assertEquals(listOf(RecordedHandOff("item", agent, "Example agent", operationId)), fixture.inbox.records)
            assertEquals("local enqueue must not record", 0, recordsAtEnqueue)

            // Reconnect reconciliation is not another delivery and must not record the same client operation again.
            fixture.messages.readyToSend.value = false
            runCurrent()
            fixture.messages.readyToSend.value = true
            runCurrent()
            assertEquals(1, fixture.messages.requests.size)
            assertEquals(1, fixture.inbox.records.size)
        } finally {
            fixture.close()
        }
    }

    @Test fun retrySuccessIsIdempotentAtTheClientCallBoundary() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        fixture.messages.answers += MessageTransportResult.HubError("input_draft", "none", retryable = false)
        try {
            val firstCall = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, firstCall.await())
            assertEquals(OutboxState.Refused, fixture.store.state(operationId))

            fixture.messages.answers += MessageTransportResult.Accepted
            assertTrue(fixture.sender.retry(operationId))
            runCurrent()
            assertEquals(OutboxState.Sent, fixture.store.state(operationId))
            assertEquals(2, fixture.messages.requests.size)
            assertEquals(listOf(operationId, operationId), fixture.messages.requests.map { it.clientMessageId })
            assertEquals(operationId, fixture.messages.requests.first().opId)
            assertNotEquals(operationId, fixture.messages.requests.last().opId)
            assertEquals(listOf(RecordedHandOff("item", agent, "Example agent", operationId)), fixture.inbox.records)

            // A caller retry uses the same request id. Durable enqueue and the hand-off record are both idempotent;
            // the transport's per-attempt operation id never replaces the original client_id on the record.
            val repeatedCall = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, repeatedCall.await())
            assertEquals(2, fixture.messages.requests.size)
            assertEquals(1, fixture.store.entries.value.size)
            assertEquals(listOf(RecordedHandOff("item", agent, "Example agent", operationId)), fixture.inbox.records)
        } finally {
            fixture.close()
        }
    }

    @Test fun recordFailureStaysQuietAndKeepsMessageDelivered() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        fixture.messages.answers += MessageTransportResult.Accepted
        fixture.inbox.onRecord = { _, _, _, _ ->
            throw IOException("record failed")
        }
        try {
            val handOff = async { fixture.repository.handOff(request) }
            runCurrent()
            assertEquals(agent, handOff.await())
            assertEquals(OutboxState.Sent, fixture.store.state(operationId))
            assertEquals(1, fixture.inbox.records.size)
            assertFalse(fixture.sender.retry(operationId))
            runCurrent()
            assertEquals(1, fixture.messages.requests.size)
            assertEquals(OutboxState.Sent, fixture.store.state(operationId))
        } finally {
            fixture.close()
        }
    }

    @Test fun legacyMainHandOffAndOrdinaryOutboxMessagesDoNotRecordAgentHandOff() = runTest {
        val fixture = HandOffFixture(backgroundScope, pairing, online = true)
        val main = SessionKey("hub-machine", "main")
        fixture.sender.connectMainConversation(object : MainConversationResolver {
            override suspend fun findMainConversation() = main
            override suspend fun createMainConversation(): SessionKey = error("unused")
        })
        fixture.messages.answers += MessageTransportResult.Accepted
        fixture.messages.answers += MessageTransportResult.Accepted
        try {
            val handOff = async {
                fixture.repository.handOff(InboxHandOffRequest("item", "Summarize", operationId = "main-client"))
            }
            runCurrent()
            assertNull(handOff.await())
            assertEquals(OutboxState.Sent, fixture.store.state("main-client"))
            assertEquals(main, fixture.messages.requests.single().session)

            val ordinary = SessionKey("laptop", "ordinary")
            fixture.sender.send(ordinary, "Ordinary chat message", "ordinary-client")
            runCurrent()
            assertEquals(OutboxState.Sent, fixture.store.state("ordinary-client"))
            assertEquals(listOf("main-client", "ordinary-client"), fixture.messages.requests.map { it.clientMessageId })
            assertTrue(fixture.inbox.records.isEmpty())
        } finally {
            fixture.close()
        }
    }

    private class HandOffFixture(owner: CoroutineScope, pairing: HubScope, online: Boolean) {
        val events = mutableListOf<String>()
        val store = IdempotentOutboxStore()
        val messages = ScriptedMessageTransport(online, events)
        val sender = OutboxMessageSender(pairing, store, messages, owner)
        val inbox = RecordingInboxTransport(pairing.path, events)
        val repository = RealInboxRepository(
            object : InboxAssistantConnection {
                override val current = MutableStateFlow<InboxAssistantTransport?>(inbox)
            },
            // The journal may still be finishing a quiet record failure when a test ends; its temporary directory stays.
            InboxReplyJournal(Files.createTempDirectory("inbox-handoff-delivery").toFile(), pairing.path),
            FakeSessions(),
            EmptyInteractionCenter,
            owner,
            mainConversationSender = sender,
            // Production injects ConnectionRuntime.messages, a grouped MessageSender facade, not the concrete outbox.
            messageSender = GroupedMessageSender(sender),
        ) { _, _ -> error("unused") }

        suspend fun close() = sender.close()
    }

    private class GroupedMessageSender(sender: OutboxMessageSender) : MessageSender by sender

    private class ScriptedMessageTransport(
        online: Boolean,
        private val events: MutableList<String>,
    ) : MessageTransport {
        override val readyToSend = MutableStateFlow(online)
        override val supportsClientOps = false
        val requests = mutableListOf<MessageRequest>()
        val answers = ArrayDeque<MessageTransportResult>()
        val evidence = mutableMapOf<String, MessageEvidence>()
        var onSend: (suspend (MessageRequest) -> MessageTransportResult)? = null

        override suspend fun reconcile(request: MessageRequest, createdAt: Long, allowTextMatch: Boolean): MessageEvidence? =
            evidence[request.clientMessageId]

        override suspend fun send(request: MessageRequest): MessageTransportResult {
            requests += request
            events += "send:${request.clientMessageId}"
            val result = onSend?.invoke(request) ?: answers.removeFirst()
            if (result == MessageTransportResult.Accepted) events += "accepted:${request.clientMessageId}"
            return result
        }
    }

    private data class RecordedHandOff(
        val itemId: String,
        val session: SessionKey,
        val title: String,
        val clientId: String,
    )

    private class RecordingInboxTransport(
        override val scope: String,
        private val events: MutableList<String>,
    ) : InboxAssistantTransport {
        val records = mutableListOf<RecordedHandOff>()
        var onRecord: suspend (String, SessionKey, String, String) -> JsonObject = { _, _, _, _ -> JsonObject(emptyMap()) }

        override suspend fun recordAgentHandOff(
            itemId: String,
            session: SessionKey,
            title: String,
            clientId: String,
        ): JsonObject {
            records += RecordedHandOff(itemId, session, title, clientId)
            events += "record:$clientId"
            return onRecord(itemId, session, title, clientId)
        }

        override suspend fun inbox() = buildJsonObject { put("items", JsonArray(emptyList())) }
        override suspend fun setDone(itemId: String, done: Boolean) = JsonObject(emptyMap())
        override suspend fun reply(itemId: String?, feedId: String?, text: String, clientId: String) = JsonObject(emptyMap())
        override suspend fun messages(feedId: String, around: Long?, before: String?, after: String?) = error("unused")
        override suspend fun assistant() = JsonObject(emptyMap())
        override suspend fun createConversation(main: Boolean) = JsonObject(emptyMap())
        override suspend fun pins() = JsonObject(emptyMap())
        override suspend fun mutes() = buildJsonObject {
            put("spaces", JsonArray(emptyList()))
            put("sessions", JsonArray(emptyList()))
        }
        override suspend fun media(ref: String) = byteArrayOf()
    }

    /** RoomOutboxStore's client-id behavior over the shared in-memory store, including main-conversation binding. */
    private class IdempotentOutboxStore : OutboxStore {
        private val delegate = MemoryOutboxStore()
        override val entries = delegate.entries

        fun state(id: String) = delegate.state(id)

        override suspend fun enqueue(entry: OutboxEntry) {
            val existing = entries.value.firstOrNull { it.clientMessageId == entry.clientMessageId }
            if (existing == null) {
                delegate.enqueue(entry)
                return
            }
            require(existing.mainTarget == entry.mainTarget &&
                (entry.mainTarget || existing.session == entry.session) &&
                existing.text == entry.text && existing.clearDraft == entry.clearDraft &&
                existing.launcher == entry.launcher && existing.model == entry.model) {
                "Operation id reused with different parameters"
            }
        }

        override suspend fun mainConversation() = delegate.mainConversation()
        override suspend fun claimMainCreation() = delegate.claimMainCreation()
        override suspend fun bindMainConversation(session: SessionKey) = delegate.bindMainConversation(session)
        override suspend fun releaseMainCreation() = delegate.releaseMainCreation()
        override suspend fun forgetMainConversation(session: SessionKey) = delegate.forgetMainConversation(session)
        override suspend fun transition(id: String, from: Set<OutboxState>, to: OutboxState, error: String?) =
            delegate.transition(id, from, to, error)
        override suspend fun transition(
            id: String,
            from: Set<OutboxState>,
            to: OutboxState,
            error: String?,
            opId: String,
        ) = delegate.transition(id, from, to, error, opId)
        override suspend fun recoverInterrupted() = delegate.recoverInterrupted()
    }

    private object EmptyInteractionCenter : InteractionCenter {
        override val pending = MutableStateFlow<List<Interaction>>(emptyList())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }
}
