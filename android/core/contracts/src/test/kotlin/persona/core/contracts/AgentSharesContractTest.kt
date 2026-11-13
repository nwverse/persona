package persona.core.contracts

import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.*

/** Every sharing DTO read from and written to the shared fixture (`sharing/agent-sharing.json`, a copy of spec/fixtures). */
class AgentSharesContractTest {
    private val fixture: JsonObject = AgentShareJson.parseToJsonElement(
        requireNotNull(javaClass.classLoader?.getResource("sharing/agent-sharing.json")).readText(),
    ).jsonObject

    private fun <T> read(key: String, serializer: KSerializer<T>): T = AgentShareJson.decodeFromJsonElement(serializer, fixture.getValue(key))
    private fun <T> write(serializer: KSerializer<T>, value: T): JsonElement = AgentShareJson.encodeToJsonElement(serializer, value)

    @Test fun catalogsKeepCountsReasonCodesAndDenials() {
        val catalog = read("catalog", ShareCatalog.serializer())
        assertTrue(catalog.canShare); assertNull(catalog.reason); assertTrue(catalog.canIncludeFuture)
        assertEquals("synthetic-snapshot", catalog.snapshotId); assertEquals("synthetic-snapshot-version", catalog.snapshotVersion)
        assertTrue(catalog.complete); assertEquals(2, catalog.totalMessages); assertEquals(1, catalog.omittedMessages)
        assertEquals(listOf("unknown_provenance"), catalog.omissions)
        assertEquals(listOf("synthetic-catalog-a", "synthetic-catalog-b"), catalog.messages.map { it.messageId })
        assertTrue(catalog.messages.all { it.selectable }); assertNull(catalog.nextCursor)
        val denied = read("denied_catalog", ShareCatalog.serializer())
        assertFalse(denied.canShare); assertEquals("ownership_unbound", denied.reason); assertFalse(denied.canIncludeFuture)
        assertTrue(denied.messages.isEmpty()); assertNull(denied.snapshotId)
    }

    @Test fun previewCarriesTheHubsTransportWarning() {
        val preview = read("preview", SharePreview.serializer())
        assertEquals("synthetic-preview", preview.previewId); assertEquals(1, preview.policyVersion)
        assertEquals(2, preview.selectedCount); assertEquals(1, preview.omittedCount)
        assertTrue(preview.requiresInsecureTransportAck)
        assertTrue(preview.transportWarning!!.startsWith("HTTP sends the bearer link"))
    }

    @Test fun requestsEncodeExactlyAsTheFixture() {
        assertEquals(fixture.getValue("create_preview_request"), write(SharePreviewRequest.serializer(),
            SharePreviewRequest("create", "synthetic-snapshot", "synthetic-snapshot-version", ShareSelection.All, false, false, 3)))
        assertEquals(fixture.getValue("create_request"), write(ShareCreateRequest.serializer(), ShareCreateRequest(
            "50000000-0000-4000-8000-000000000001", "synthetic-machine", "synthetic-session", "synthetic-snapshot",
            "synthetic-snapshot-version", ShareSelection.All, includeFuture = false, showTools = false, expiresInDays = 3,
            acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "synthetic-preview", insecureTransport = true))))
        assertEquals(fixture.getValue("narrow_patch"), write(ShareUpdateRequest.serializer(),
            ShareUpdateRequest(1, removeMessageIds = listOf("synthetic-editor-a"), showTools = false, includeFuture = false)))
        assertEquals(fixture.getValue("revoke_request"), write(ShareRevokeRequest.serializer(), ShareRevokeRequest(2)))
        // The three selection shapes are mutually exclusive objects tagged by mode.
        assertEquals("""{"mode":"from","from_message_id":"m"}""", AgentShareJson.encodeToString(ShareSelection.serializer(), ShareSelection.From("m")))
        assertEquals("""{"mode":"individual","message_ids":["a","b"]}""",
            AgentShareJson.encodeToString(ShareSelection.serializer(), ShareSelection.Individual(listOf("a", "b"))))
    }

    @Test fun metadataPagesExecutionAndRevocationDecode() {
        val share = read("share", ShareMetadata.serializer())
        assertEquals("synthetic-share", share.shareId); assertEquals(1L, share.revision); assertEquals(1L, share.contentVersion)
        assertEquals(ShareSelectionSummary("all", 2), share.selection)
        assertFalse(share.includeFuture); assertFalse(share.showTools)
        assertEquals("active", share.status); assertEquals("frozen", share.publicationStatus); assertNull(share.pauseReason)
        val page = read("public_page", ShareMessages.serializer())
        assertEquals(1L, page.revision); assertEquals("synthetic-public-a", page.messages.single().messageId)
        val tool = read("tool_message", ShareMessage.serializer())
        val execution = tool.execution!!
        assertEquals(ShareExecution.Text("Synthetic intermediate narration"), execution[0])
        assertEquals(ShareExecution.Tool("synthetic-public-tool", "command", "[REDACTED]", listOf(ShareToolResult("Synthetic tool result"))), execution[1])
        assertEquals(ShareExecution.Agent("synthetic-public-agent", "Synthetic associated child result"), execution[2])
        assertNull(read("public_page", ShareMessages.serializer()).messages.single().execution)
        assertEquals(ShareRevoked("synthetic-share", 3, "revoked"), read("revoked", ShareRevoked.serializer()))
    }

    @Test fun createdLinkNeverAppearsInToString() {
        val created = ShareCreated(read("share", ShareMetadata.serializer()), "https://share.example.test/share/" + "a".repeat(64))
        assertFalse(created.toString().contains("share.example.test"))
    }
}
