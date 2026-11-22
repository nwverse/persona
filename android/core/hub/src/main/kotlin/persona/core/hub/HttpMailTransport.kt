package persona.core.hub

import persona.core.contracts.api.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import java.io.OutputStream
import kotlinx.serialization.json.*
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

class HttpMailTransport(
    override val scope: String,
    baseUrl: String,
    private val token: String,
    client: OkHttpClient,
    private val isCurrent: () -> Boolean = { true },
) : MailTransport {
    private val base = baseUrl.toHttpUrl()
    private val codec = Json { ignoreUnknownKeys = true; coerceInputValues = true }
    private val http = client.newBuilder().followRedirects(false).followSslRedirects(false)
        .retryOnConnectionFailure(false).build()

    private fun url(vararg parts: String) = base.newBuilder().apply { parts.forEach(::addPathSegment) }.build()
    private fun checkScope() { if (!isCurrent()) throw CancellationException("Pairing changed") }
    private suspend fun request(url: HttpUrl, body: JsonObject? = null): JsonObject {
        checkScope()
        val request = Request.Builder().url(url.withRequestManager()).header("Authorization", "Bearer $token").apply {
            body?.let { post(it.toString().toRequestBody("application/json".toMediaType())) }
        }.build()
        return http.newCall(request).awaitResponse().use {
            checkScope()
            if (!it.isSuccessful) throw HubFailure.from(it)
            codec.parseToJsonElement(requireNotNull(it.body).string()).jsonObject
        }
    }

    override suspend fun item(itemId: String): MailInboxItem =
        codec.decodeFromJsonElement(request(url("api", "v1", "inbox", itemId)))

    override suspend fun message(ref: MailReference, uid: Long): MailMessage {
        require(uid in 1..4294967295L && uid in ref.uids && ref.accountId.isNotBlank())
        val response = request(url("api", "v1", "mail", "messages", ref.accountId, uid.toString())
            .newBuilder().addQueryParameter("mailbox", ref.mailbox).build())
        return codec.decodeFromJsonElement<MailMessage>(requireNotNull(response["message"])).also {
            require(it.uid == uid && it.accountId == ref.accountId && it.mailbox == ref.mailbox)
        }
    }

    override suspend fun setFlags(ref: MailReference, uid: Long, seen: Boolean?, flagged: Boolean?) {
        require(uid in 1..4294967295L && uid in ref.uids && ref.accountId.isNotBlank())
        val body = buildJsonObject {
            put("mailbox", ref.mailbox)
            seen?.let { put("seen", it) }
            flagged?.let { put("flagged", it) }
        }
        request(url("api", "v1", "mail", "messages", ref.accountId, uid.toString(), "flags"), body)
    }

    override suspend fun setSeen(ref: MailReference, uid: Long, seen: Boolean) {
        setFlags(ref, uid, seen = seen)
    }

    override suspend fun setFlagged(ref: MailReference, uid: Long, flagged: Boolean) {
        setFlags(ref, uid, flagged = flagged)
    }

    override suspend fun accounts(): List<MailAccount> {
        val response = request(url("api", "v1", "mail", "accounts"))
        val accountsElement = response["accounts"] ?: return emptyList()
        return codec.decodeFromJsonElement<List<MailAccount>>(accountsElement)
    }

    override suspend fun sendMail(request: SendMailRequest) {
        require(request.accountId.isNotBlank())
        request(url("api", "v1", "mail", "send"), codec.encodeToJsonElement(request).jsonObject)
    }

    override suspend fun refreshInbox() {
        request(url("api", "v1", "inbox", "refresh"), buildJsonObject { })
    }

    override suspend fun downloadAttachment(ref: MailReference, uid: Long, attachmentId: String, output: OutputStream,
        offset: Long, progress: (Long, Long?) -> Unit) = withContext(Dispatchers.IO) {
        require(uid in 1..4294967295L && uid in ref.uids && ref.accountId.isNotBlank() && attachmentId.isNotBlank() && offset >= 0)
        checkScope()
        val target = url("api", "v1", "mail", "messages", ref.accountId, uid.toString(), "attachments", attachmentId)
            .newBuilder().addQueryParameter("mailbox", ref.mailbox).build()
        val request = Request.Builder().url(target.withRequestManager()).header("Authorization", "Bearer $token")
            .header("Accept-Encoding", "identity").apply { if (offset > 0) header("Range", "bytes=$offset-") }.build()
        http.newCall(request).awaitResponse().use { response ->
            checkScope()
            if (!response.isSuccessful) throw HubFailure.from(response)
            if (offset > 0) require(response.code == 206 && response.header("Content-Range")?.startsWith("bytes $offset-") == true)
            val body = requireNotNull(response.body)
            val size = body.contentLength().takeIf { it >= 0 }
            val total = size?.plus(offset)
            var read = offset
            progress(read, total)
            body.byteStream().use { input ->
                val buffer = ByteArray(32 * 1024)
                while (true) {
                    currentCoroutineContext().ensureActive()
                    checkScope()
                    val count = input.read(buffer)
                    if (count < 0) break
                    output.write(buffer, 0, count)
                    read += count
                    progress(read, total)
                }
            }
            checkScope()
            require(total == null || read == total) { "Incomplete attachment" }
        }
    }
}
