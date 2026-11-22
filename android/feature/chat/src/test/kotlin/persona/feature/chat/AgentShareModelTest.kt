package persona.feature.chat

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import java.time.Instant

internal fun syntheticShare(status: String = "active") = ShareMetadata("sealed", 1, 1, ShareSelectionSummary("all", 101),
    false, false, Instant.now().toString(), Instant.now().toString(), Instant.now().plusSeconds(3 * 86400).toString(), status, "frozen")

internal open class ShareModelApi : AgentShares {
    var current = true
    var createCount = 0
    var previewRequests = mutableListOf<SharePreviewRequest>()
    var updateRequest: ShareUpdateRequest? = null
    var canWiden = true
    var failCreate = false
    var insecureTransport = false
    var createRequest: ShareCreateRequest? = null
    var previewGate: CompletableDeferred<Unit>? = null
    var share = syntheticShare()
    var liveShares = listOf(share)
    override fun requireCurrent() { if (!current) throw AgentShareFailure("authority_changed") }
    override suspend fun catalog(snapshotId: String?, cursor: String?) = ShareCatalog(true, canIncludeFuture = true,
        snapshotId = "snapshot", snapshotVersion = "v1", expiresAt = "2099-01-01T00:00:00Z", complete = true,
        totalMessages = 101, omittedMessages = 1, omissions = listOf("unknown_provenance"),
        messages = listOf(ShareCatalogMessage(if (cursor == null) "recent" else "older", "user", "Synthetic input", true)),
        nextCursor = if (cursor == null) "older-page" else null)
    override suspend fun preview(request: SharePreviewRequest): SharePreview {
        previewRequests += request; previewGate?.await()
        return SharePreview("preview-${previewRequests.size}", "2099-01-01T00:00:00Z", 1, 101, 1,
            listOf(ShareMessage("public-local", "assistant", "Synthetic final result")), "next-preview", requiresInsecureTransportAck = insecureTransport)
    }
    override suspend fun previewPage(previewId: String, cursor: String) = SharePreview(previewId, "2099-01-01T00:00:00Z", 1, 101, 1,
        listOf(ShareMessage("public-old", "user", "Synthetic old input")), requiresInsecureTransportAck = insecureTransport)
    override suspend fun create(request: ShareCreateRequest): ShareCreated {
        createCount++; createRequest = request
        if (failCreate) throw AgentShareFailure("request_failed")
        return ShareCreated(share, "${if (insecureTransport) "http" else "https"}://share.example.test/share/${"a".repeat(64)}")
    }
    override suspend fun list() = ShareList(liveShares)
    override suspend fun get(shareId: String) = ShareDetail(share, canWiden)
    override suspend fun messages(shareId: String, cursor: String?) = ShareMessages(share.revision, share.contentVersion,
        listOf(ShareMessage(if (cursor == null) "editor-recent" else "editor-old", "user", "Synthetic sealed input")), if (cursor == null) "older-sealed" else null)
    override suspend fun update(shareId: String, request: ShareUpdateRequest): ShareResult { updateRequest = request; return ShareResult(share.copy(revision = 2)) }
    override suspend fun revoke(shareId: String, expectedRevision: Long): ShareRevoked {
        assertEquals(share.revision, expectedRevision); liveShares = emptyList(); return ShareRevoked(shareId, 2, "revoked")
    }
}

class AgentShareModelTest {
    @Test fun listDescribesOnlyActiveLiveFuturePublicationNotTheRetainedPreference() {
        for (status in listOf("active", "revoked", "expired")) {
            for (publication in listOf("live", "paused", "frozen")) {
                for (future in listOf(false, true)) {
                    val share = syntheticShare(status).copy(includeFuture = future, publicationStatus = publication)
                    assertEquals("$status / $publication / $future", status == "active" && publication == "live" && future,
                        publishesFutureMessages(share))
                }
            }
        }
    }

    @Test fun independentHttpLinkRequiresTransportConsentForExactPreviewAndPolicyChangesClearBothConsents() = runTest {
        val api = ShareModelApi().apply { insecureTransport = true }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.newShare(); runCurrent(); model.moreCatalog(); runCurrent()
        model.mode("individual"); model.select("older")
        model.preparePreview(); runCurrent(); model.acknowledge(true)
        assertFalse(model.confirmed)
        model.create(); runCurrent(); assertEquals(0, api.createCount)
        model.preparePreview(); runCurrent(); model.acknowledge(true); model.acknowledgeTransport(true)
        assertTrue(model.confirmed)
        model.lifetime(7); assertFalse(model.acknowledged); assertFalse(model.transportAcknowledged)
        model.preparePreview(); runCurrent(); model.acknowledge(true); model.acknowledgeTransport(true)
        model.create(); runCurrent()
        assertEquals(ShareSelection.Individual(listOf("older")), api.createRequest!!.selection)
        assertEquals(7, api.createRequest!!.expiresInDays)
        assertTrue(api.createRequest!!.acknowledgement.insecureTransport)
        assertTrue(model.publicLink!!.startsWith("http://"))
        model.close()
    }

    @Test fun defaultsCompleteCatalogSelectionOlderPagesAndExactAcknowledgement() = runTest {
        val api = ShareModelApi(); val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.newShare(); runCurrent()
        assertFalse(model.includeFuture); assertFalse(model.showTools); assertEquals(3, model.days)
        assertEquals(101, model.catalog!!.totalMessages)
        model.moreCatalog(); runCurrent(); model.mode("from"); model.select("older")
        model.preparePreview(); runCurrent()
        assertEquals(ShareSelection.From("older"), api.previewRequests.single().selection)
        assertEquals(2, model.previewMessages.size)
        model.acknowledge(true); model.tools(true)
        assertFalse(model.acknowledged); assertNull(model.preview)
        model.create(); runCurrent(); assertEquals(0, api.createCount)
        model.preparePreview(); runCurrent(); model.acknowledge(true); model.create(); model.create(); runCurrent()
        assertEquals(1, api.createCount); assertNotNull(model.publicLink)
        var copied: String? = null; model.copyLink { copied = it }
        assertTrue(copied!!.startsWith("https://"))
        // The link stays on screen after copying, until the user leaves it; then it is gone for good.
        assertEquals(copied, model.publicLink)
        model.finishLink(); assertNull(model.publicLink); assertEquals(AgentSharePage.List, model.page)
        model.close()
    }
    @Test fun uncertainCreationRecoveryDoesNotRequireRevokingPreviouslyVerifiedExistingLinks() = runTest {
        val api = ShareModelApi().apply { failCreate = true }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.refreshList(); runCurrent(); model.newShare(); runCurrent()
        model.preparePreview(); runCurrent(); model.acknowledge(true); model.create(); runCurrent()
        assertTrue(model.uncertainCreation)
        val original = api.liveShares.single()
        api.liveShares = listOf(original, original.copy(shareId = "uncertain-new"))
        model.refreshList(); runCurrent(); model.newShare(); runCurrent()
        assertTrue(model.uncertainCreation)
        api.liveShares = listOf(original)
        model.refreshList(); runCurrent(); model.newShare(); runCurrent()
        assertFalse(model.uncertainCreation)
        assertEquals(1, api.createCount)
        assertEquals("active", api.liveShares.single().status)
        model.close()
    }

    @Test fun lostCreateNeverRetriesAndRecoveryRequiresTokenlessInspectionAndRevocation() = runTest {
        val api = ShareModelApi().apply { failCreate = true }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.newShare(); runCurrent(); model.preparePreview(); runCurrent(); model.acknowledge(true); model.create(); runCurrent()
        model.create(); runCurrent(); assertEquals(1, api.createCount); assertTrue(model.uncertainCreation)
        model.newShare(); runCurrent(); assertTrue(model.uncertainCreation)
        model.refreshList(); runCurrent(); model.newShare(); runCurrent(); assertTrue(model.uncertainCreation)
        model.edit("sealed"); runCurrent(); model.revoke(); runCurrent(); model.refreshList(); runCurrent(); model.newShare(); runCurrent()
        assertFalse(model.uncertainCreation); assertEquals(1, api.createCount)
        model.close()
    }
    @Test fun offlineNarrowingUsesEditorIdsAndRevisionWithoutSourceCatalogOrPreview() = runTest {
        val api = object : ShareModelApi() {
            override suspend fun catalog(snapshotId: String?, cursor: String?): ShareCatalog = error("Offline narrowing must not reach source")
            override suspend fun preview(request: SharePreviewRequest): SharePreview = error("Safe narrowing requires no preview")
        }.apply { canWiden = false; share = share.copy(includeFuture = true, showTools = true) }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.edit("sealed"); runCurrent(); model.moreSealed(); runCurrent(); model.remove("editor-old")
        model.future(false); model.tools(false); model.lifetime(1); assertFalse(model.needsPreview)
        model.save(); runCurrent()
        val request = api.updateRequest!!
        assertEquals(listOf("editor-old"), request.removeMessageIds)
        assertEquals(1L, request.expectedRevision); assertNull(request.acknowledgement)
        assertNull(request.snapshotId); assertNull(request.selection)
        assertEquals(false, request.includeFuture); assertEquals(false, request.showTools)
        model.close()
    }
    @Test fun wideningAndFutureReenableBindShareContentVersionAndCreationBasedExpiry() = runTest {
        val api = ShareModelApi(); val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.edit("sealed"); runCurrent(); model.future(true); model.tools(true); model.lifetime(30)
        assertTrue(model.needsPreview)
        model.save(); runCurrent(); assertNull(api.updateRequest)
        model.preparePreview(); runCurrent(); val target = api.previewRequests.single()
        assertEquals("update", target.operation); assertEquals(1L, target.expectedRevision); assertEquals(1L, target.expectedContentVersion)
        assertNull(target.snapshotId); assertNull(target.selection)
        assertEquals(Instant.parse(api.share.createdAt).plusSeconds(30L * 86400).toString(), target.expiresAt)
        model.acknowledge(true); model.save(); runCurrent()
        assertEquals(target.expiresAt, api.updateRequest!!.expiresAt)
        assertEquals("preview-1", api.updateRequest!!.acknowledgement!!.previewId)
        model.close()
    }
    @Test fun pausedFuturePreferenceRequiresExplicitReactivationAndFreshExactConsent() = runTest {
        val api = ShareModelApi().apply {
            insecureTransport = true
            share = share.copy(includeFuture = true, publicationStatus = "paused", pauseReason = "source_incomplete")
        }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.edit("sealed"); runCurrent()
        assertTrue(model.canResumeFuture); assertFalse(model.changed); assertFalse(model.needsPreview)
        model.save(); runCurrent(); assertNull(api.updateRequest)
        // A preview obtained before the explicit resume action cannot authorize reactivation.
        model.preparePreview(); runCurrent(); model.acknowledge(true); model.acknowledgeTransport(true)
        model.resumeFuture()
        assertTrue(model.includeFuture); assertTrue(model.changed); assertTrue(model.needsPreview)
        assertNull(model.preview); assertFalse(model.confirmed)
        model.save(); runCurrent(); assertNull(api.updateRequest)
        model.preparePreview(); runCurrent()
        val target = api.previewRequests.last()
        assertEquals("update", target.operation); assertEquals(true, target.includeFuture)
        assertEquals(1L, target.expectedRevision); assertEquals(1L, target.expectedContentVersion)
        assertNull(target.selection); assertNull(target.snapshotId)
        model.acknowledge(true); assertFalse(model.confirmed)
        model.acknowledgeTransport(true); model.save(); runCurrent()
        val request = api.updateRequest!!
        assertEquals(true, request.includeFuture); assertEquals(1L, request.expectedContentVersion)
        assertEquals("preview-2", request.acknowledgement!!.previewId)
        assertTrue(request.acknowledgement!!.insecureTransport)
        assertNull(request.selection); assertNull(request.removeMessageIds)
        model.close()
    }

    @Test fun reactivationIsUnavailableOfflineOrAfterExpiryAndCannotMixWithMessageRemovals() = runTest {
        val api = ShareModelApi().apply { share = share.copy(includeFuture = true, publicationStatus = "paused") }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.edit("sealed"); runCurrent(); model.remove("editor-recent"); model.resumeFuture()
        assertFalse(model.resumingFuture); assertFalse(model.needsPreview)
        model.remove("editor-recent"); model.resumeFuture(); assertTrue(model.resumingFuture)
        model.future(false); assertFalse(model.resumingFuture); assertFalse(model.needsPreview)
        api.canWiden = false; model.edit("sealed"); runCurrent()
        assertFalse(model.canResumeFuture); model.resumeFuture(); assertFalse(model.changed)
        api.canWiden = true; api.share = api.share.copy(status = "expired"); model.edit("sealed"); runCurrent()
        assertFalse(model.canResumeFuture); model.resumeFuture(); assertFalse(model.changed)
        model.close()
    }

    @Test fun closeDuringPreviewAndAuthorityChangeClearLateContentAndCapability() = runTest {
        val api = ShareModelApi().apply { previewGate = CompletableDeferred() }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.newShare(); runCurrent(); model.preparePreview(); runCurrent(); model.close(); api.previewGate!!.complete(Unit); runCurrent()
        assertNull(model.preview); assertTrue(model.previewMessages.isEmpty())
        val other = ShareModelApi(); val active = AgentShareModel(other, SessionKey("machine", "session"), this)
        active.newShare(); runCurrent(); active.preparePreview(); runCurrent(); active.acknowledge(true); active.create(); runCurrent()
        other.current = false; var copies = 0; active.copyLink { copies++ }
        assertEquals(0, copies); assertNull(active.publicLink); assertTrue(active.catalogMessages.isEmpty())
        active.close()
    }
    @Test fun revisionConflictInvalidatesConfirmationAndCannotReviveExpiredShares() = runTest {
        val api = object : ShareModelApi() {
            override suspend fun update(shareId: String, request: ShareUpdateRequest): ShareResult = throw AgentShareFailure("revision_conflict", 409)
        }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.edit("sealed"); runCurrent(); model.future(true); model.preparePreview(); runCurrent(); model.acknowledge(true)
        model.save(); runCurrent(); assertFalse(model.acknowledged); assertNull(model.preview)
        api.share = api.share.copy(status = "expired"); model.edit("sealed"); runCurrent(); assertFalse(model.editable)
        model.save(); runCurrent(); assertNull(api.updateRequest)
        model.close()
    }

    @Test fun selectedCountFollowsTheCompleteCatalogForEveryMode() = runTest {
        val api = ShareModelApi(); val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.newShare(); runCurrent(); model.moreCatalog(); runCurrent()
        assertEquals(101, model.selectedCount)
        model.mode("from"); assertEquals(ShareSelection.From("recent"), model.selection); assertEquals(101, model.selectedCount)
        model.select("older"); assertEquals(100, model.selectedCount)
        model.mode("individual"); assertEquals(0, model.selectedCount); assertFalse(model.validSelection)
        model.select("recent"); model.select("older"); assertEquals(2, model.selectedCount)
        model.select("recent"); assertEquals(ShareSelection.Individual(listOf("older")), model.selection)
        model.close()
    }

    @Test fun deniedCatalogKeepsItsReasonAndNeverPreviewsOrCreates() = runTest {
        val api = object : ShareModelApi() {
            override suspend fun catalog(snapshotId: String?, cursor: String?) = ShareCatalog(false, "ownership_unbound")
        }
        val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.newShare(); runCurrent()
        assertFalse(model.catalog!!.canShare); assertEquals("ownership_unbound", model.catalog!!.reason)
        assertEquals(failureText("ownership_unbound"), persona.core.designsystem.UiText.Res(R.string.chat_agent_share_ownership))
        model.preparePreview(); runCurrent(); model.acknowledge(true); model.create(); runCurrent()
        assertTrue(api.previewRequests.isEmpty()); assertEquals(0, api.createCount); assertNull(model.preview)
        model.close()
    }

    @Test fun revokeUsesTheCurrentRevisionAndRevisionConflictAsksForARefresh() = runTest {
        val api = ShareModelApi(); val model = AgentShareModel(api, SessionKey("machine", "session"), this)
        model.refreshList(); runCurrent(); model.edit("sealed"); runCurrent()
        model.revoke(); runCurrent()
        assertEquals("revoked", model.shares.single().status); assertEquals(AgentSharePage.List, model.page)
        val stale = object : ShareModelApi() {
            override suspend fun revoke(shareId: String, expectedRevision: Long): ShareRevoked = throw AgentShareFailure("revision_conflict", 409)
        }
        val other = AgentShareModel(stale, SessionKey("machine", "session"), this)
        other.refreshList(); runCurrent(); other.edit("sealed"); runCurrent(); other.revoke(); runCurrent()
        assertEquals(persona.core.designsystem.UiText.Res(R.string.chat_agent_share_conflict), other.notice)
        assertEquals("active", other.shares.single().status)
        model.close(); other.close()
    }
}
