package dev.persona.watch.data

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.SocketTimeoutException
import java.net.URL
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.Base64
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLException
import javax.net.ssl.X509TrustManager

data class HubResponse(val status: Int, val body: String)

/** Small injectable transport keeps wire-contract tests independent of an Android device. */
fun interface HubTransport {
    fun request(config: HubConfig, method: String, path: String, body: String?, headers: Map<String, String>): HubResponse
}

class UrlConnectionTransport : HubTransport {
    override fun request(config: HubConfig, method: String, path: String, body: String?, headers: Map<String, String>): HubResponse {
        val validated = config.validated()
        if (!path.startsWith("/api/v1/") || path.contains("\r") || path.contains("\n")) throw HubException("invalid_address")
        val connection = URL(validated.baseUrl + path).openConnection() as HttpURLConnection
        try {
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 8000
            connection.readTimeout = when { method == "GET" -> 15000; path.substringBefore('?').endsWith("/draft") -> 90000; else -> 60000 }
            connection.requestMethod = method
            connection.setRequestProperty("Authorization", "Bearer ${validated.token}")
            connection.setRequestProperty("Accept", "application/json")
            headers.forEach { (name, value) -> connection.setRequestProperty(name, value) }
            if (connection is HttpsURLConnection && validated.fingerprint != null) {
                val trust = PinTrust(validated.fingerprint)
                connection.sslSocketFactory = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), null) }.socketFactory
                // An explicit pairing pin authenticates the public key, including the hub's self-signed LAN certificate.
                connection.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, session ->
                    runCatching { trust.matches(session.peerCertificates[0] as X509Certificate) }.getOrDefault(false)
                }
            }
            if (body != null) {
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
                val bytes = body.toByteArray(Charsets.UTF_8)
                connection.setFixedLengthStreamingMode(bytes.size)
                connection.outputStream.use { it.write(bytes) }
            }
            val status = connection.responseCode
            val stream = if (status in 200..299) connection.inputStream else connection.errorStream
            val text = stream?.use { input ->
                val buffer = ByteArray(8192)
                val output = ByteArrayOutputStream()
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    if (output.size() + count > MAX_RESPONSE_BYTES) throw HubException("response_too_large")
                    output.write(buffer, 0, count)
                }
                output.toString("UTF-8")
            }.orEmpty()
            return HubResponse(status, text)
        } catch (e: HubException) { throw e }
        catch (_: SocketTimeoutException) { throw HubException("timeout") }
        catch (_: SSLException) { throw HubException("certificate_error") }
        catch (_: Exception) { throw HubException("unreachable") }
        finally { connection.disconnect() }
    }

    private class PinTrust(pin: String) : X509TrustManager {
        private val expected = Base64.getUrlDecoder().decode(pin)
        fun matches(certificate: X509Certificate): Boolean = MessageDigest.isEqual(expected,
            MessageDigest.getInstance("SHA-256").digest(certificate.publicKey.encoded))
        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
            if (chain.isNullOrEmpty() || !matches(chain[0])) throw CertificateException("Hub key mismatch")
        }
        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) { throw CertificateException("Unsupported") }
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
    }

    companion object { private const val MAX_RESPONSE_BYTES = 4 * 1024 * 1024 }
}

/** Every method blocks: activities call these on their worker executor, never the main thread. */
class HubClient(
    config: HubConfig,
    private val locale: String = "zh-Hans",
    private val transport: HubTransport = UrlConnectionTransport(),
) {
    val config: HubConfig = config.validated()

    /** Exchange a one-time/full pairing token for a revocable, answer-only watch credential. */
    fun pair(): HubConfig {
        val reply = request("POST", "/api/v1/auth/tokens", JSONObject().put("name", "Samsung Watch").put("kind", "watch"))
        if (reply.text("role") != "answer" || reply.text("id").isBlank() || reply.text("token").isBlank())
            throw HubException("invalid_watch_credential")
        return config.copy(token = reply.text("token"), deviceId = reply.text("id")).validated()
    }

    fun refresh(): WatchSnapshot {
        val sessions = WatchParser.sessions(request("GET", "/api/v1/sessions"))
        val interactions = WatchParser.interactions(request("GET", "/api/v1/interactions"), sessions)
        val machines = WatchParser.machines(request("GET", "/api/v1/machines"))
        var unavailable = false
        val inbox = try { WatchParser.inbox(request("GET", "/api/v1/inbox?sources=all")) }
        catch (failure: HubException) {
            if (failure.status == 401) throw failure
            unavailable = true
            emptyList()
        }
        return WatchParser.snapshot(sessions, interactions, machines, inbox, System.currentTimeMillis(), unavailable)
    }

    fun messages(session: WatchSession, labels: WatchTextLabels = WatchTextLabels.empty()): List<WatchMessage> = WatchParser.messages(request("GET",
        sessionPath(session) + "/items?turns=2&limit=80&lazy_images=1&fold_work=1"), labels)

    /** Called only when a person expands a work summary; folded ranges remain off the watch until then. */
    fun work(session: WatchSession, message: WatchMessage): List<String> = message.work.flatMap { entry ->
        val start = entry.fromSeq ?: return@flatMap listOf(entry.text).filter { it.isNotBlank() }
        val through = entry.throughSeq ?: return@flatMap listOf(entry.text).filter { it.isNotBlank() }
        val lines = mutableListOf<String>()
        var from: Long = start
        repeat(100) {
            val page = request("GET", sessionPath(session) + "/items?from_seq=$from&through_seq=$through&limit=1000&lazy_images=1")
            val items = page.requiredRows("items")
            lines += items.mapNotNull { WatchParser.workItem(it)?.text?.takeIf(String::isNotBlank) }
            if (!page.optBoolean("more")) return@flatMap lines
            val last = items.maxOfOrNull { it.optLong("seq", -1) } ?: throw HubException("invalid_response")
            if (last < from) throw HubException("invalid_response")
            from = last + 1
            if (from > through) return@flatMap lines
        }
        throw HubException("response_too_large")
    }

    fun send(session: WatchSession, text: String, clientMessageId: String) {
        if (!session.canSend) throw HubException("read_only")
        checkText(text)
        val body = request("POST", sessionPath(session) + "/messages",
            JSONObject().put("text", text).put("client_msg_id", checkedOperationId(clientMessageId)), clientMessageId)
        if (body.text("state") in setOf("pending", "unknown")) throw HubException("delivery_unconfirmed")
    }

    fun interrupt(session: WatchSession) {
        if (!session.canInterrupt) throw HubException("read_only")
        request("POST", sessionPath(session) + "/interrupt", JSONObject())
    }

    fun markSeen(session: WatchSession) { request("POST", sessionPath(session) + "/seen", JSONObject()) }

    fun resolve(interaction: WatchInteraction, resolution: JSONObject, operationId: String): ResolveResult {
        WatchResolution.validate(interaction, resolution)
        return try {
            val result = request("POST", api("interactions", interaction.id, "resolve"), resolution, operationId)
            if (result.text("state") in setOf("pending", "unknown")) throw HubException("delivery_unconfirmed")
            ResolveResult(already = result.optBoolean("already"))
        } catch (failure: HubException) {
            if (failure.status == 410) ResolveResult(gone = true) else throw failure
        }
    }

    fun markDone(item: InboxItem) {
        request("POST", inboxPath(item, "done"), JSONObject().put("done", true))
    }

    fun reply(item: InboxItem, text: String, operationId: String) {
        if (!item.canReply) throw HubException("read_only")
        checkText(text)
        val receipt = request("POST", inboxPath(item, "reply"), JSONObject().put("text", text), operationId)
        if (receipt.text("state") in setOf("pending", "unknown")) throw HubException("delivery_unconfirmed")
    }

    /** Generate a suggestion only. Sending remains a separate, explicit user action. */
    fun draftReply(item: InboxItem): String {
        if (!item.canReply) throw HubException("read_only")
        return request("POST", inboxPath(item, "draft"), JSONObject().put("instruction", ""))
            .text("text").also { if (it.isBlank() || it.length > 16000) throw HubException("invalid_response") }
    }

    private fun inboxPath(item: InboxItem, action: String): String = api("inbox", item.id, action) +
        (item.manager?.let { "?manager=${encode(it)}" } ?: "")

    private fun request(method: String, path: String, body: JSONObject? = null, operationId: String? = null): JSONObject {
        val headers = mutableMapOf("Accept-Language" to locale.takeIf { it.matches(Regex("[A-Za-z-]{2,35}")) }.orEmpty().ifEmpty { "en" })
        if (operationId != null) headers["X-Persona-Op-Id"] = checkedOperationId(operationId)
        val result = transport.request(config, method, path, body?.toString(), headers)
        if (result.status !in 200..299) {
            val code = when (result.status) {
                401 -> "pairing_expired"; 403 -> "forbidden"; 404 -> "not_found"; 409 -> "changed"; 410 -> "gone"
                in 300..399 -> "redirect_refused"; 429 -> "busy"; in 500..599 -> "hub_unavailable"; else -> "request_failed"
            }
            throw HubException(code, result.status)
        }
        if (result.body.isBlank()) return JSONObject()
        return try { JSONObject(result.body) } catch (_: Exception) { throw HubException("invalid_response") }
    }

    companion object {
        private fun encode(value: String) = URLEncoder.encode(value, "UTF-8").replace("+", "%20")
        private fun api(vararg segments: String): String {
            if (segments.any { it.isEmpty() || it == "." || it == ".." || it.any { c -> c.code < 32 } }) throw HubException("invalid_response")
            return "/api/v1/" + segments.joinToString("/") { encode(it) }
        }
        private fun sessionPath(session: WatchSession) = api("sessions", session.machineId, session.sessionId)
        private fun checkedOperationId(id: String): String {
            if (!id.matches(Regex("[A-Za-z0-9_-]{8,128}"))) throw HubException("invalid_operation")
            return id
        }
        private fun checkText(text: String) {
            if (text.isBlank() || text.length > 16000) throw HubException("invalid_message")
        }
    }
}
