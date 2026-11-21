package dev.persona.watch.data

import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.Base64
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

/** A pairing pin authenticates the Hub public key, including self-signed LAN certificates. */
object PinTLS {
    fun configure(connection: HttpsURLConnection, pin: String?) {
        if (pin == null) return
        val expected = try { Base64.getUrlDecoder().decode(pin) } catch (_: Exception) { throw HubException("invalid_fingerprint") }
        if (expected.size != 32) throw HubException("invalid_fingerprint")
        fun matches(certificate: X509Certificate) = MessageDigest.isEqual(expected,
            MessageDigest.getInstance("SHA-256").digest(certificate.publicKey.encoded))
        val trust = object : X509TrustManager {
            override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
                if (chain.isNullOrEmpty() || !matches(chain[0])) throw CertificateException("Hub key mismatch")
            }
            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) { throw CertificateException("Unsupported") }
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        }
        connection.sslSocketFactory = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trust), null) }.socketFactory
        connection.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, session ->
            runCatching { matches(session.peerCertificates[0] as X509Certificate) }.getOrDefault(false)
        }
    }
}
