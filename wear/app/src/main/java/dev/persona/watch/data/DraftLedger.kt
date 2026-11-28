package dev.persona.watch.data

import java.util.UUID

internal interface DraftStorage {
    fun get(key: String): String?
    fun put(values: Map<String, String?>)
}

/** Captured storage keys and revisions never follow a later connection selection. */
internal class DraftLedger(private val storage: DraftStorage, private val newId: () -> String = { UUID.randomUUID().toString() }) {
    data class Version(val hash: String, val revision: String?, val text: String)
    data class Submission(val version: Version, val text: String, val operationId: String, val legacyKey: String)
    fun text(hash: String) = storage.get("draft:$hash").orEmpty()
    fun version(hash: String) = Version(hash, storage.get("revision:$hash"), text(hash))
    fun save(hash: String, text: String) {
        if (text == text(hash)) return
        storage.put(mapOf("draft:$hash" to text, "revision:$hash" to newId(), "send-operation:$hash" to null))
    }
    fun submit(hash: String, legacyKey: String): Submission {
        val version = version(hash)
        val operation = storage.get("send-operation:$hash")
            ?: (if (version.revision == null) storage.get(legacyKey) else null)
            ?: newId()
        storage.put(mapOf("send-operation:$hash" to operation))
        return Submission(version, version.text.trim(), operation, legacyKey)
    }
    fun matches(version: Version) = version(version.hash) == version
    fun acknowledge(submission: Submission): Boolean {
        if (!matches(submission.version) || storage.get("send-operation:${submission.version.hash}") != submission.operationId) return false
        val hash = submission.version.hash
        val changes = mutableMapOf<String, String?>("draft:$hash" to null, "revision:$hash" to null, "send-operation:$hash" to null)
        if (storage.get(submission.legacyKey) == submission.operationId) changes[submission.legacyKey] = null
        storage.put(changes)
        return true
    }
    fun suggest(version: Version, text: String): Boolean {
        if (!matches(version)) return false
        save(version.hash, text)
        return true
    }

    /** Blank/equal drafts need no replacement consent; a changed nonblank draft needs its exact version. */
    fun acceptSuggestion(hash: String, suggestion: String, confirmed: Version? = null): Boolean {
        if (suggestion.isBlank()) return false
        val current = version(hash)
        if (current.text.isNotBlank() && current.text != suggestion && current != confirmed) return false
        save(hash, suggestion)
        // Equal text was already saved; a storage failure must not discard the pending suggestion.
        return text(hash) == suggestion
    }
}
