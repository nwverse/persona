package persona.core.domain.tasks

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import persona.core.contracts.FeedItem

/** How an interrupted receipt's task was dealt with, as the hub worked it out (#3834). */
enum class InterruptResolution(val wire: String) {
    Pending("pending"), Cancelled("cancelled"), Rerouted("rerouted"), Resumed("resumed"), Accepted("accepted"), Done("done");

    companion object {
        fun of(wire: String?): InterruptResolution? = entries.firstOrNull { it.wire == wire }
    }
}

/** One task of a batch: its receipt, to-do, title (when cut off) and resolution. */
data class InterruptBatchTask(val receipt: String, val todoId: String, val title: String, val resolution: InterruptResolution)

/** Interruptions with the same cause in one chat within ten minutes, as their first receipt carries them. */
data class InterruptBatch(val cause: String, val machine: String?, val total: Int, val pending: Int, val resolved: Int,
                          val tasks: List<InterruptBatchTask>)

/**
 * How the chat draws an interrupted receipt (docs/protocol/protocol.md "Tasks and the chat feed"): [Hidden] inside
 * its batch's head, [Batch] the head of several, [Resolved] one dealt with (a grey line), [Pending] still to deal with.
 * The hub works it all out; the apps only draw it.
 */
sealed interface InterruptLook {
    data object Hidden : InterruptLook
    data class Batch(val batch: InterruptBatch) : InterruptLook
    data class Resolved(val resolution: InterruptResolution) : InterruptLook
    data object Pending : InterruptLook

    companion object {
        /** Null for anything but an interrupted receipt from a hub that sends `resolution` (drawn as ever). */
        fun of(item: FeedItem): InterruptLook? {
            if (item.kind != "receipt" || item.field("state") != "interrupted") return null
            val resolution = InterruptResolution.of(item.field("resolution")) ?: return null
            item.field("group")?.takeIf { it.isNotEmpty() && it != item.id }?.let { return Hidden }
            batch(item)?.takeIf { it.tasks.size > 1 }?.let { return Batch(it) }
            return if (resolution == InterruptResolution.Pending) Pending else Resolved(resolution)
        }

        private fun batch(item: FeedItem): InterruptBatch? {
            val o = (item.body as? JsonObject)?.get("batch") as? JsonObject ?: return null
            fun JsonObject.str(k: String) = (this[k] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull
            fun JsonObject.int(k: String) = (this[k] as? JsonPrimitive)?.intOrNull ?: 0
            val tasks = (o["tasks"] as? JsonArray).orEmpty().mapNotNull { e ->
                val t = e as? JsonObject ?: return@mapNotNull null
                // a resolution this build doesn't know reads as handled, not as pending
                InterruptBatchTask(t.str("receipt").orEmpty(), t.str("todo_id").orEmpty(), t.str("title").orEmpty(),
                    InterruptResolution.of(t.str("resolution")) ?: InterruptResolution.Done)
            }
            return InterruptBatch(o.str("cause") ?: "model_error", o.str("machine")?.takeIf(String::isNotEmpty), o.int("total"),
                o.int("pending"), o.int("resolved"), tasks)
        }
    }
}
