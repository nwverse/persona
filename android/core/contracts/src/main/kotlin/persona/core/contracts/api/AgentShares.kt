package persona.core.contracts.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import persona.core.contracts.SessionKey

/** The one codec for sharing bodies: unknown fields are ignored, absent optional fields are omitted rather than sent as null. */
val AgentShareJson: kotlinx.serialization.json.Json = kotlinx.serialization.json.Json {
    ignoreUnknownKeys = true; encodeDefaults = true; explicitNulls = false
}

/** Captures the pairing, route and explicit session manager before any sharing request suspends. */
fun interface AgentSharesProvider {
    fun forSession(session: SessionKey, manager: String): AgentShares
}

/** Tokenless management except for the one-time create response. Never persist or log that response. */
interface AgentShares {
    suspend fun catalog(snapshotId: String? = null, cursor: String? = null): ShareCatalog
    suspend fun preview(request: SharePreviewRequest): SharePreview
    suspend fun previewPage(previewId: String, cursor: String): SharePreview
    suspend fun create(request: ShareCreateRequest): ShareCreated
    suspend fun list(): ShareList
    suspend fun get(shareId: String): ShareDetail
    suspend fun messages(shareId: String, cursor: String? = null): ShareMessages
    suspend fun update(shareId: String, request: ShareUpdateRequest): ShareResult
    suspend fun revoke(shareId: String, expectedRevision: Long): ShareRevoked
    /** Rejects stale UI actions, including copying a link after an authority change. */
    fun requireCurrent()
}

@OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
@kotlinx.serialization.json.JsonClassDiscriminator("mode")
@Serializable
sealed interface ShareSelection {
    @Serializable @SerialName("all") data object All : ShareSelection
    @Serializable @SerialName("from") data class From(@SerialName("from_message_id") val fromMessageId: String) : ShareSelection
    @Serializable @SerialName("individual") data class Individual(@SerialName("message_ids") val messageIds: List<String>) : ShareSelection
}

@OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
@kotlinx.serialization.json.JsonClassDiscriminator("kind")
@Serializable
sealed interface ShareExecution {
    @Serializable @SerialName("text") data class Text(val text: String) : ShareExecution
    @Serializable @SerialName("tool") data class Tool(
        @SerialName("tool_id") val toolId: String, val name: String, val arguments: String,
        val results: List<ShareToolResult> = emptyList(),
    ) : ShareExecution
    @Serializable @SerialName("agent") data class Agent(@SerialName("tool_id") val toolId: String, val text: String) : ShareExecution
}
@Serializable data class ShareToolResult(val text: String)
@Serializable data class ShareMessage(
    @SerialName("message_id") val messageId: String, val role: String, val text: String,
    val execution: List<ShareExecution>? = null,
)
@Serializable data class ShareCatalogMessage(
    @SerialName("message_id") val messageId: String, val role: String, val text: String, val selectable: Boolean,
)
@Serializable data class ShareCatalog(
    @SerialName("can_share") val canShare: Boolean,
    val reason: String? = null,
    @SerialName("can_include_future") val canIncludeFuture: Boolean = false,
    @SerialName("snapshot_id") val snapshotId: String? = null,
    @SerialName("snapshot_version") val snapshotVersion: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    val complete: Boolean = false,
    @SerialName("total_messages") val totalMessages: Int = 0,
    @SerialName("omitted_messages") val omittedMessages: Int = 0,
    /** Structural reason codes (for example `unknown_provenance`), each listed once. */
    val omissions: List<String> = emptyList(),
    val messages: List<ShareCatalogMessage> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
)
@Serializable data class ShareSelectionSummary(val mode: String, val count: Int)
@Serializable data class ShareMetadata(
    @SerialName("share_id") val shareId: String,
    val revision: Long,
    @SerialName("content_version") val contentVersion: Long,
    val selection: ShareSelectionSummary,
    @SerialName("include_future") val includeFuture: Boolean,
    @SerialName("show_tools") val showTools: Boolean,
    @SerialName("created_at") val createdAt: String,
    @SerialName("updated_at") val updatedAt: String,
    @SerialName("expires_at") val expiresAt: String,
    val status: String,
    @SerialName("publication_status") val publicationStatus: String,
    @SerialName("pause_reason") val pauseReason: String? = null,
)
@Serializable data class ShareAcknowledgement(
    @SerialName("sensitive_content") val sensitiveContent: Boolean = true,
    @SerialName("policy_version") val policyVersion: Int,
    @SerialName("preview_id") val previewId: String,
    @SerialName("insecure_transport") val insecureTransport: Boolean = false,
)
@Serializable data class SharePreviewRequest(
    val operation: String,
    @SerialName("snapshot_id") val snapshotId: String? = null,
    @SerialName("snapshot_version") val snapshotVersion: String? = null,
    val selection: ShareSelection? = null,
    @SerialName("include_future") val includeFuture: Boolean,
    @SerialName("show_tools") val showTools: Boolean,
    @SerialName("expires_in_days") val expiresInDays: Int? = null,
    @SerialName("share_id") val shareId: String? = null,
    @SerialName("expected_revision") val expectedRevision: Long? = null,
    @SerialName("expected_content_version") val expectedContentVersion: Long? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
)
@Serializable data class SharePreview(
    @SerialName("preview_id") val previewId: String,
    @SerialName("expires_at") val expiresAt: String,
    @SerialName("policy_version") val policyVersion: Int,
    @SerialName("selected_count") val selectedCount: Int,
    @SerialName("omitted_count") val omittedCount: Int,
    val messages: List<ShareMessage> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
    @SerialName("requires_insecure_transport_ack") val requiresInsecureTransportAck: Boolean = false,
    /** The hub's localized plain-text warning; present only when [requiresInsecureTransportAck]. */
    @SerialName("transport_warning") val transportWarning: String? = null,
)
@Serializable data class ShareCreateRequest(
    @SerialName("client_request_id") val clientRequestId: String,
    val machine: String,
    @SerialName("session_id") val sessionId: String,
    @SerialName("snapshot_id") val snapshotId: String,
    @SerialName("snapshot_version") val snapshotVersion: String,
    val selection: ShareSelection,
    @SerialName("include_future") val includeFuture: Boolean = false,
    @SerialName("show_tools") val showTools: Boolean = false,
    @SerialName("expires_in_days") val expiresInDays: Int = 3,
    val acknowledgement: ShareAcknowledgement,
)
/** Not a data class: generated toString must never contain the capability. */
@Serializable class ShareCreated(val share: ShareMetadata, @SerialName("public_url") val publicUrl: String) {
    override fun toString(): String = "ShareCreated(redacted)"
}
@Serializable data class ShareList(val shares: List<ShareMetadata> = emptyList())
@Serializable data class ShareDetail(val share: ShareMetadata, @SerialName("can_widen") val canWiden: Boolean, val reason: String? = null)
@Serializable data class ShareMessages(
    val revision: Long, @SerialName("content_version") val contentVersion: Long,
    val messages: List<ShareMessage> = emptyList(), @SerialName("next_cursor") val nextCursor: String? = null,
)
@Serializable data class ShareUpdateRequest(
    @SerialName("expected_revision") val expectedRevision: Long,
    @SerialName("expected_content_version") val expectedContentVersion: Long? = null,
    @SerialName("remove_message_ids") val removeMessageIds: List<String>? = null,
    @SerialName("snapshot_id") val snapshotId: String? = null,
    @SerialName("snapshot_version") val snapshotVersion: String? = null,
    val selection: ShareSelection? = null,
    @SerialName("show_tools") val showTools: Boolean? = null,
    @SerialName("include_future") val includeFuture: Boolean? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    val acknowledgement: ShareAcknowledgement? = null,
)
@Serializable data class ShareResult(val share: ShareMetadata)
@Serializable data class ShareRevokeRequest(@SerialName("expected_revision") val expectedRevision: Long)
@Serializable data class ShareRevoked(@SerialName("share_id") val shareId: String, val revision: Long, val status: String)

/** Safe structural codes only: no response body, URL, receipt or exception cause is retained. */
class AgentShareFailure(override val code: String, override val status: Int? = null) :
    Exception("Agent sharing request failed"), HubCodedFailure
