package dev.persona.watch.data

import java.net.URI
import java.net.URLDecoder

/** A copied Persona pairing link carries one explicit Hub origin and its TLS pin, never a redirect. */
object PairingInput {
    fun parse(addressOrLink: String, separateToken: String): HubConfig {
        val address = addressOrLink.trim()
        if (!address.startsWith("persona:")) return HubConfig(address, separateToken.trim()).canonicalized()
        try {
            val link = URI(address)
            if (link.scheme != "persona" || link.host != "pair" || link.rawUserInfo != null ||
                link.rawFragment != null || link.rawPath.orEmpty().isNotEmpty() || link.port != -1) throw HubException("invalid_address")
            val values = linkedMapOf<String, String>()
            for (entry in link.rawQuery.orEmpty().split('&')) {
                val parts = entry.split('=', limit = 2)
                if (parts.size != 2) throw HubException("invalid_address")
                val key = URLDecoder.decode(parts[0], "UTF-8")
                val value = URLDecoder.decode(parts[1], "UTF-8")
                if (values.put(key, value) != null) throw HubException("invalid_address")
            }
            val token = values["token"] ?: throw HubException("invalid_token")
            if (separateToken.isNotBlank() && separateToken.trim() != token) throw HubException("invalid_token")
            var origin = URI(values["url"] ?: throw HubException("invalid_address"))
            val pin = values["fp"]?.takeIf { it.isNotEmpty() }
            if (origin.scheme.equals("http", ignoreCase = true) && pin != null) {
                val port = values["tls"]?.toIntOrNull()?.takeIf { it in 1..65535 } ?: throw HubException("invalid_address")
                // Validate before rebuilding so userinfo, query and fragments cannot disappear during normalization.
                HubConfig(origin.toString(), token).validated()
                origin = URI("https", null, origin.host, port, origin.path, null, null)
            }
            return HubConfig(origin.toString(), token, pin).canonicalized()
        } catch (failure: HubException) { throw failure }
        catch (_: Exception) { throw HubException("invalid_address") }
    }
}
