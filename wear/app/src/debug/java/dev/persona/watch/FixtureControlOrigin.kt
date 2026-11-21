package dev.persona.watch

import java.net.URI
import java.net.URL

/** Fault-injection controls may use only the known loopback fixture, never an arbitrary authority. */
internal class FixtureControlOrigin private constructor(private val host: String, private val port: Int) {
    fun url(path: String): URL {
        check(path in setOf("/api/v1/health", "/__test")) { "fixture_control_invalid_path" }
        // Rebuild from validated components; do not concatenate the original raw authority.
        return URI("http", null, host, port, path, null, null).toURL()
    }

    companion object {
        private const val FIXTURE_PORT = 18765
        private val authorityPattern = Regex("(127\\.0\\.0\\.1|localhost|\\[::1\\])(?::([0-9]+))?", RegexOption.IGNORE_CASE)

        fun parse(baseUrl: String): FixtureControlOrigin {
            val origin = try { URI(baseUrl) } catch (_: Exception) { invalid() }
            if (origin.scheme != "http" || origin.rawUserInfo != null || origin.rawQuery != null ||
                origin.rawFragment != null || origin.rawPath.orEmpty() !in setOf("", "/")) invalid()
            // Brackets delimit the IPv6 host; only a following colon can introduce the port.
            val authority = authorityPattern.matchEntire(origin.rawAuthority.orEmpty()) ?: invalid()
            val host = authority.groupValues[1].lowercase().removePrefix("[").removeSuffix("]")
            val portText = authority.groupValues[2]
            val port = if (portText.isEmpty()) -1 else portText.toIntOrNull()?.takeIf { it == FIXTURE_PORT } ?: invalid()
            val parsedHost = origin.host?.lowercase()?.removePrefix("[")?.removeSuffix("]")
            if (parsedHost != host || origin.port != port) invalid()
            return FixtureControlOrigin(host, port)
        }

        private fun invalid(): Nothing = error("fixture_control_requires_loopback_root")
    }
}
