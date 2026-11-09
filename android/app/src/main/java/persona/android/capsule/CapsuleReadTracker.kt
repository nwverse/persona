package persona.android.capsule

import persona.core.contracts.AgentState
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatSnapshot

/** A metadata revision cannot grant a receipt for an older, unchanged reply window. */
internal class CapsuleReadTracker {
    private var key: SessionKey? = null
    private var reply: Pair<String, String?>? = null
    private var capturedVersion: String? = null
    private var workingReply = false
    private var completedVersion: String? = null

    fun version(target: SessionKey?, snapshot: ChatSnapshot, current: Session?): String? {
        if (key != target) {
            key = target; reply = null; capturedVersion = null; workingReply = false; completedVersion = null
        }
        if (target == null || current == null || !snapshot.verified || snapshot.loading || snapshot.error != null || snapshot.ended || snapshot.replaced) return null
        val index = snapshot.items.indexOfLast { it.role == "assistant" && it.kind == "text" && !it.text.isNullOrBlank() }
        if (index < 0 || index < snapshot.items.indexOfLast { it.role == "user" && it.kind == "text" }) return null
        val latest = snapshot.items[index]
        val preview = current.lastText?.trim()?.removeSuffix("…")?.replace(Regex("""\s+"""), " ")
        val text = latest.text.orEmpty().trim().replace(Regex("""\s+"""), " ")
        if (!preview.isNullOrBlank() && !text.startsWith(preview)) return null
        val signature = latest.id to latest.text
        val version = "${current.updatedAt.orEmpty()}|${current.rev ?: ""}"
        if (signature != reply) {
            reply = signature
            capturedVersion = version
            workingReply = current.agentState == AgentState.Working
            completedVersion = null
        }
        if (current.agentState != AgentState.Unread) return null
        if (capturedVersion != version && workingReply && completedVersion == null) {
            if (!preview.isNullOrBlank()) completedVersion = version
        }
        return version.takeIf { it == capturedVersion || it == completedVersion }?.let { "${capsuleId(target)}|$it" }
    }
}
