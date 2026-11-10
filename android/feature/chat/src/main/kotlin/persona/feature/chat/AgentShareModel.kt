package persona.feature.chat

import androidx.compose.runtime.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.ensureActive
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.UiText
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID

enum class AgentSharePage { List, New, Editor }

/** A sheet-local, nonpersistent controller. The capability exists only until copied or this sheet is disposed. */
@Stable
class AgentShareModel(private val api: AgentShares, private val session: SessionKey, parentScope: CoroutineScope) {
    private val lifetime = kotlinx.coroutines.SupervisorJob(parentScope.coroutineContext[kotlinx.coroutines.Job])
    private val scope = CoroutineScope(parentScope.coroutineContext + lifetime)
    var page by mutableStateOf(AgentSharePage.List); private set
    var busy by mutableStateOf(false); private set
    var notice by mutableStateOf<UiText?>(null); private set
    var shares by mutableStateOf<List<ShareMetadata>>(emptyList()); private set
    var catalog by mutableStateOf<ShareCatalog?>(null); private set
    var catalogMessages by mutableStateOf<List<ShareCatalogMessage>>(emptyList()); private set
    var selection by mutableStateOf<ShareSelection>(ShareSelection.All); private set
    var includeFuture by mutableStateOf(false); private set
    var showTools by mutableStateOf(false); private set
    var days by mutableIntStateOf(3); private set
    var preview by mutableStateOf<SharePreview?>(null); private set
    var previewMessages by mutableStateOf<List<ShareMessage>>(emptyList()); private set
    var acknowledged by mutableStateOf(false); private set
    var transportAcknowledged by mutableStateOf(false); private set
    val confirmed: Boolean get() = acknowledged && preview?.let { !it.requiresInsecureTransportAck || transportAcknowledged } == true
    var detail by mutableStateOf<ShareDetail?>(null); private set
    var sealedMessages by mutableStateOf<List<ShareMessage>>(emptyList()); private set
    var sealedCursor by mutableStateOf<String?>(null); private set
    var removed by mutableStateOf<Set<String>>(emptySet()); private set
    var replacingSelection by mutableStateOf(false); private set
    var expiry by mutableStateOf<String?>(null); private set
    var publicLink by mutableStateOf<String?>(null); private set
    var uncertainCreation by mutableStateOf(false); private set
    private var listVerified = false
    private var creationBaseline: Set<String>? = null
    private var submitted = false
    private var epoch = 0L
    private var exactPreview: SharePreviewRequest? = null
    private val seenCatalogCursors = mutableSetOf<String>()
    private val seenSealedCursors = mutableSetOf<String>()

    private fun action(block: suspend () -> Unit) {
        if (busy) return
        busy = true
        val captured = epoch
        scope.launch {
            try {
                checkCurrent()
                block()
                checkCurrent()
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Exception) {
                if (captured == epoch) {
                    invalidatePreview()
                    notice = failureText((error as? HubCodedFailure)?.code)
                    if ((error as? HubCodedFailure)?.code == "authority_changed") clearContent()
                }
            } finally { if (captured == epoch) busy = false }
        }
    }
    private fun checkCurrent() { lifetime.ensureActive(); api.requireCurrent() }
    fun close() { epoch++; lifetime.cancel(); publicLink = null; clearContent() }
    private fun clearContent() {
        invalidatePreview()
        shares = emptyList(); catalog = null; catalogMessages = emptyList(); detail = null
        sealedMessages = emptyList(); previewMessages = emptyList(); publicLink = null
    }
    private fun invalidatePreview() { preview = null; previewMessages = emptyList(); exactPreview = null; acknowledged = false; transportAcknowledged = false }
    fun acknowledge(value: Boolean) { if (!busy && preview != null) acknowledged = value }
    fun acknowledgeTransport(value: Boolean) { if (!busy && preview?.requiresInsecureTransportAck == true) transportAcknowledged = value }
    private fun separateNarrowing(widening: Boolean): Boolean {
        if (widening && removed.isNotEmpty()) { notice = UiText.Res(R.string.chat_agent_share_separate_changes); return true }
        return false
    }
    fun future(value: Boolean) { if (!busy && !separateNarrowing(value && detail?.share?.includeFuture == false)) { includeFuture = value; invalidatePreview() } }
    fun tools(value: Boolean) { if (!busy && !separateNarrowing(value && detail?.share?.showTools == false)) { showTools = value; invalidatePreview() } }
    fun lifetime(value: Int) {
        if (busy || value !in listOf(1, 3, 7, 30)) return
        val target = detail?.share?.let { Instant.parse(it.createdAt).plus(value.toLong(), ChronoUnit.DAYS) }
        if (target != null && !target.isAfter(Instant.now())) { notice = UiText.Res(R.string.chat_agent_share_expiry_past); return }
        if (target != null && separateNarrowing(target.isAfter(Instant.parse(requireNotNull(detail).share.expiresAt)))) return
        days = value; expiry = target?.toString(); invalidatePreview()
    }
    fun mode(value: String) {
        if (busy) return
        selection = when (value) {
            "from" -> catalogMessages.firstOrNull { it.selectable }?.let { ShareSelection.From(it.messageId) } ?: ShareSelection.Individual(emptyList())
            "individual" -> ShareSelection.Individual(emptyList())
            else -> ShareSelection.All
        }
        invalidatePreview()
    }
    fun select(id: String) {
        if (busy || catalogMessages.none { it.messageId == id && it.selectable }) return
        selection = when (val old = selection) {
            is ShareSelection.From -> ShareSelection.From(id)
            is ShareSelection.Individual -> {
                val ids = old.messageIds.toMutableList()
                if (!ids.remove(id)) { if (ids.size >= 1000) { notice = UiText.Res(R.string.chat_agent_share_selection_limit); return }; ids += id }
                ShareSelection.Individual(ids)
            }
            ShareSelection.All -> return
        }
        invalidatePreview()
    }
    fun remove(id: String) {
        if (!busy && sealedMessages.any { it.messageId == id }) {
            if (needsPreview) { notice = UiText.Res(R.string.chat_agent_share_separate_changes); return }
            if (id !in removed && removed.size >= 1000) { notice = UiText.Res(R.string.chat_agent_share_selection_limit); return }
            removed = if (id in removed) removed - id else removed + id
            invalidatePreview()
        }
    }
    fun refreshList() = action {
        val response = api.list(); checkCurrent()
        shares = response.shares; listVerified = true; page = AgentSharePage.List; publicLink = null
    }
    fun newShare() {
        if (busy) return
        if (uncertainCreation && (!listVerified || shares.any { it.status == "active" && it.shareId !in creationBaseline.orEmpty() })) {
            notice = UiText.Res(R.string.chat_agent_share_lost); return
        }
        page = AgentSharePage.New; detail = null; removed = emptySet(); replacingSelection = false
        includeFuture = false; showTools = false; days = 3; expiry = null; selection = ShareSelection.All
        submitted = false; uncertainCreation = false; publicLink = null; notice = null; invalidatePreview()
        loadCatalog()
    }
    fun loadCatalog() = action {
        val response = api.catalog(); checkCurrent()
        // A denied catalog's reason is shown with the catalog itself, so it survives later notices.
        catalog = response; catalogMessages = response.messages; seenCatalogCursors.clear(); invalidatePreview()
    }
    fun moreCatalog() = action {
        val current = catalog ?: return@action
        val cursor = current.nextCursor ?: return@action
        if (!seenCatalogCursors.add(cursor) || catalogMessages.size >= 10000) throw AgentShareFailure("source_limit")
        val response = api.catalog(current.snapshotId, cursor); checkCurrent()
        if (!response.canShare || response.snapshotId != current.snapshotId || response.snapshotVersion != current.snapshotVersion)
            throw AgentShareFailure("share_changed")
        catalogMessages = (catalogMessages + response.messages).distinctBy { it.messageId }; catalog = response
        if (catalogMessages.size > 10000) throw AgentShareFailure("source_limit")
    }
    fun edit(shareId: String) = action {
        val response = api.get(shareId)
        val messages = api.messages(shareId); checkCurrent()
        if (messages.revision != response.share.revision || messages.contentVersion != response.share.contentVersion) throw AgentShareFailure("share_changed")
        detail = response; sealedMessages = messages.messages; sealedCursor = messages.nextCursor; seenSealedCursors.clear()
        page = AgentSharePage.Editor; removed = emptySet(); replacingSelection = false; catalog = null; catalogMessages = emptyList()
        includeFuture = response.share.includeFuture; showTools = response.share.showTools; expiry = response.share.expiresAt
        publicLink = null; notice = null; invalidatePreview()
    }
    fun moreSealed() = action {
        val current = detail?.share ?: return@action
        val cursor = sealedCursor ?: return@action
        if (!seenSealedCursors.add(cursor) || sealedMessages.size >= 10000) throw AgentShareFailure("source_limit")
        val response = api.messages(current.shareId, cursor); checkCurrent()
        if (response.revision != current.revision || response.contentVersion != current.contentVersion) throw AgentShareFailure("share_changed")
        sealedMessages = (sealedMessages + response.messages).distinctBy { it.messageId }; sealedCursor = response.nextCursor
        if (sealedMessages.size > 10000) throw AgentShareFailure("source_limit")
    }
    fun replaceSelection() {
        if (busy || detail?.canWiden != true || separateNarrowing(true)) return
        replacingSelection = true; removed = emptySet(); selection = ShareSelection.All; invalidatePreview(); loadCatalog()
    }
    fun keepSelection() { if (!busy) { replacingSelection = false; invalidatePreview() } }
    val needsPreview: Boolean get() {
        val old = detail?.share ?: return true
        return replacingSelection || (showTools && !old.showTools) || (includeFuture && !old.includeFuture) ||
            expiry?.let { Instant.parse(it).isAfter(Instant.parse(old.expiresAt)) } == true
    }
    /** Whether the editor holds anything to save. */
    val changed: Boolean get() {
        val old = detail?.share ?: return false
        return replacingSelection || removed.isNotEmpty() || showTools != old.showTools || includeFuture != old.includeFuture ||
            (expiry != null && expiry != old.expiresAt)
    }
    val editable: Boolean get() = detail?.share?.status == "active"
    /**
     * How many catalog messages the selection covers, against the complete catalog: All is every message, From counts
     * that message and every later one (pages arrive oldest first), Individual its picks. Null while unknown.
     */
    val selectedCount: Int? get() {
        val current = catalog ?: return null
        return when (val picked = selection) {
            ShareSelection.All -> current.totalMessages
            is ShareSelection.Individual -> picked.messageIds.size
            is ShareSelection.From -> catalogMessages.indexOfFirst { it.messageId == picked.fromMessageId }
                .takeIf { it >= 0 }?.let { current.totalMessages - it }
        }
    }
    val validSelection: Boolean get() = when (val picked = selection) {
        is ShareSelection.Individual -> picked.messageIds.isNotEmpty()
        is ShareSelection.From -> picked.fromMessageId.isNotBlank()
        ShareSelection.All -> true
    }
    private fun previewRequest(): SharePreviewRequest {
        val current = detail?.share
        val snapshot = catalog.takeIf { current == null || replacingSelection }
        if ((current == null || replacingSelection) && (snapshot?.canShare != true || !snapshot.complete || !validSelection)) throw AgentShareFailure("source_incomplete")
        return SharePreviewRequest(if (current == null) "create" else "update",
            snapshot?.snapshotId, snapshot?.snapshotVersion, selection.takeIf { snapshot != null },
            includeFuture, showTools, days.takeIf { current == null }, current?.shareId, current?.revision,
            current?.contentVersion, expiry.takeIf { current != null })
    }
    fun preparePreview() = action {
        val target = previewRequest()
        val first = api.preview(target)
        val messages = first.messages.toMutableList()
        val cursors = mutableSetOf<String>()
        var cursor = first.nextCursor
        while (cursor != null) {
            if (!cursors.add(cursor) || messages.size >= 10000) throw AgentShareFailure("source_limit")
            val next = api.previewPage(first.previewId, cursor)
            if (next.previewId != first.previewId || next.policyVersion != first.policyVersion || next.expiresAt != first.expiresAt ||
                next.selectedCount != first.selectedCount || next.omittedCount != first.omittedCount || next.requiresInsecureTransportAck != first.requiresInsecureTransportAck)
                throw AgentShareFailure("share_changed")
            messages += next.messages; cursor = next.nextCursor
        }
        checkCurrent()
        if (messages.size > 10000) throw AgentShareFailure("source_limit")
        preview = first; previewMessages = messages; exactPreview = target; acknowledged = false; transportAcknowledged = false; notice = null
    }
    private fun acknowledgement(): ShareAcknowledgement {
        val prepared = preview ?: throw AgentShareFailure("preview_required")
        if (!confirmed || exactPreview != previewRequest() || !Instant.parse(prepared.expiresAt).isAfter(Instant.now()))
            throw AgentShareFailure("preview_required")
        return ShareAcknowledgement(policyVersion = prepared.policyVersion, previewId = prepared.previewId,
            insecureTransport = prepared.requiresInsecureTransportAck && transportAcknowledged)
    }
    fun create() {
        if (busy || submitted) return
        action {
            val ack = acknowledgement()
            val snapshot = catalog ?: throw AgentShareFailure("preview_required")
            val request = ShareCreateRequest(UUID.randomUUID().toString(), session.machineId, session.sessionId,
                requireNotNull(snapshot.snapshotId), requireNotNull(snapshot.snapshotVersion), selection, includeFuture, showTools, days, ack)
            creationBaseline = shares.mapTo(mutableSetOf()) { it.shareId }.takeIf { listVerified }
            submitted = true
            try {
                val response = api.create(request); checkCurrent()
                // A configured independent HTTP origin is allowed only with this preview's transport consent.
                val uri = java.net.URI(response.publicUrl)
                if (uri.scheme !in listOf("http", "https") || (uri.scheme == "http" && !ack.insecureTransport) || uri.rawUserInfo != null || uri.host.isNullOrBlank() ||
                    uri.rawQuery != null || uri.rawFragment != null || !uri.rawPath.matches(Regex("/share/[A-Za-z0-9_-]{64}")))
                    throw AgentShareFailure("invalid_response")
                publicLink = response.publicUrl; shares = listOf(response.share) + shares.filterNot { it.shareId == response.share.shareId }
                invalidatePreview(); notice = null
            } catch (error: Exception) {
                uncertainCreation = true; listVerified = false; publicLink = null; notice = UiText.Res(R.string.chat_agent_share_lost)
                if (error is CancellationException) throw error
                throw AgentShareFailure("creation_uncertain")
            }
        }
    }
    /** Copies the one-time link; it stays on screen until [finishLink] or the sheet closes, and is never stored. */
    fun copyLink(copy: (String) -> Unit) {
        try { checkCurrent(); publicLink?.let(copy); notice = UiText.Res(R.string.chat_agent_share_copied) }
        catch (_: Exception) { publicLink = null; clearContent(); notice = failureText("authority_changed") }
    }
    /** Drops the one-time link for good and returns to the tokenless list. */
    fun finishLink() { if (!busy) { publicLink = null; page = AgentSharePage.List; notice = null } }
    fun save() = action {
        val current = detail?.share ?: return@action
        if (current.status != "active") throw AgentShareFailure("share_changed")
        if (!changed) return@action
        val ack = if (needsPreview) acknowledgement() else null
        val snapshot = catalog.takeIf { replacingSelection }
        val request = ShareUpdateRequest(current.revision, current.contentVersion.takeIf { needsPreview || removed.isNotEmpty() },
            removed.toList().takeIf { it.isNotEmpty() }, snapshot?.snapshotId, snapshot?.snapshotVersion,
            selection.takeIf { replacingSelection }, showTools.takeIf { it != current.showTools },
            includeFuture.takeIf { it != current.includeFuture }, expiry?.takeIf { it != current.expiresAt }, ack)
        val result = api.update(current.shareId, request); checkCurrent()
        shares = shares.map { if (it.shareId == result.share.shareId) result.share else it }
        page = AgentSharePage.List; detail = null; invalidatePreview(); notice = UiText.Res(R.string.chat_agent_share_saved)
    }
    fun revoke() = action {
        val current = detail?.share ?: return@action
        val result = api.revoke(current.shareId, current.revision); checkCurrent()
        shares = shares.map { if (it.shareId == result.shareId) it.copy(status = result.status, revision = result.revision) else it }
        page = AgentSharePage.List; detail = null; publicLink = null; invalidatePreview(); notice = UiText.Res(R.string.chat_agent_share_revoked)
    }
}

/** Structural codes only (docs/server/agent-sharing.md); never a hub's error text. */
internal fun failureText(code: String?): UiText = UiText.Res(when (code) {
    "creation_uncertain", "creation_already_committed", "idempotency_conflict" -> R.string.chat_agent_share_lost
    "revision_conflict", "share_changed", "snapshot_changed", "not_found" -> R.string.chat_agent_share_conflict
    "authority_changed", "authority_unavailable", "session_conflict", "manager_selector_conflict" -> R.string.chat_agent_share_authority
    "ownership_unbound", "owner_enrollment_required", "not_owner" -> R.string.chat_agent_share_ownership
    "source_unsupported", "source_incomplete", "source_limit", "manager_unsupported" -> R.string.chat_agent_share_source
    "preview_required", "preview_changed", "insecure_transport_ack_required" -> R.string.chat_agent_share_preview_required
    "invalid_expiry" -> R.string.chat_agent_share_expiry_past
    "public_origin_unavailable" -> R.string.chat_agent_share_origin
    else -> R.string.chat_agent_share_failed
})
