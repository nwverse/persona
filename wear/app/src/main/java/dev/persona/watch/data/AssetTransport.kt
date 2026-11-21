package dev.persona.watch.data

import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.SocketTimeoutException
import java.net.URL
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLException

/** Binary GETs stay on the paired origin and never follow redirects carrying a credential. */
class AssetTransport {
    fun get(input: HubConfig, path: String, maxBytes: Int = 4 * 1024 * 1024): ByteArray {
        val config = input.validated()
        if (!path.matches(Regex("/api/v1/[A-Za-z0-9_./-]+")) || path.split('/').any { it == "." || it == ".." } || maxBytes !in 1..8 * 1024 * 1024) {
            throw HubException("invalid_address")
        }
        val connection = URL(config.baseUrl + path).openConnection() as HttpURLConnection
        try {
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 8000
            connection.readTimeout = 15000
            connection.setRequestProperty("Authorization", "Bearer ${config.token}")
            if (connection is HttpsURLConnection) PinTLS.configure(connection, config.fingerprint)
            val status = connection.responseCode
            if (status !in 200..299) throw HubException(when (status) {
                401 -> "pairing_expired"; 403 -> "forbidden"; 404 -> "not_found"
                in 300..399 -> "redirect_refused"; else -> "request_failed"
            }, status)
            if (connection.contentLengthLong > maxBytes) throw HubException("response_too_large")
            return connection.inputStream.use { inputStream ->
                val output = ByteArrayOutputStream()
                val buffer = ByteArray(8192)
                while (true) {
                    val length = inputStream.read(buffer)
                    if (length < 0) break
                    if (output.size() + length > maxBytes) throw HubException("response_too_large")
                    output.write(buffer, 0, length)
                }
                output.toByteArray()
            }
        } catch (failure: HubException) { throw failure }
        catch (_: SocketTimeoutException) { throw HubException("timeout") }
        catch (_: SSLException) { throw HubException("certificate_error") }
        catch (_: Exception) { throw HubException("unreachable") }
        finally { connection.disconnect() }
    }
}
