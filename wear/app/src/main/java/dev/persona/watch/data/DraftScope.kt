package dev.persona.watch.data

import java.security.MessageDigest

object DraftScope {
    /** Explicit owners remain stable when a different connection is added or removed. */
    fun ownedHash(primaryUrl: String, owner: String, key: String): String {
        if (owner.isEmpty() && (key.startsWith("inbox:") || key.startsWith("send:inbox:")) && primaryUrl.isBlank())
            throw HubException("authentication_failed")
        val scope = if (owner.isEmpty()) primaryUrl else "group:$owner"
        return digest(scope, key)
    }
    fun hash(primaryUrl: String, groups: Set<String>, key: String): String {
        if (key.startsWith("inbox:") || key.startsWith("send:inbox:")) return ownedHash(primaryUrl, "", key)
        val owner = groups.firstOrNull { group ->
            listOf("session:", "send:session:", "continue:", "resolve:").any { prefix -> key.startsWith(prefix + group + "\u0000") }
        }
        val scope = owner?.let { "group:$it" } ?: primaryUrl
        return digest(scope, key)
    }
    private fun digest(scope: String, key: String): String = MessageDigest.getInstance("SHA-256").digest((scope + "\u0000" + key).toByteArray(Charsets.UTF_8))
            .joinToString("") { "%02x".format(it) }
}
