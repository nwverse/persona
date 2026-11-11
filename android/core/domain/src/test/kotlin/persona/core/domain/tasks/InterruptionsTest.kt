package persona.core.domain.tasks

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.FeedItem

/** The chat draws an interrupted receipt only from what the hub worked out (#3834). */
class InterruptionsTest {
    private fun receipt(id: String, extra: Map<String, Any> = emptyMap(), batch: JsonObject? = null): FeedItem {
        val body = buildJsonObject {
            put("title", "Fix the login page")
            put("state", "interrupted")
            extra.forEach { (k, v) -> put(k, JsonPrimitive(v.toString())) }
            batch?.let { put("batch", it) }
        }
        return FeedItem(id = id, conversation = "conv1", at = "2026-11-11T00:30:00Z", kind = "receipt", ref = "todo_$id", body = body)
    }

    @Test fun aReceiptFromAnOlderHubIsDrawnAsEver() {
        assertNull(InterruptLook.of(receipt("a")))
    }

    @Test fun cancelledReroutedAndResumedAreHandledLines() {
        for (r in listOf(InterruptResolution.Cancelled, InterruptResolution.Rerouted, InterruptResolution.Resumed, InterruptResolution.Accepted)) {
            assertEquals(InterruptLook.Resolved(r), InterruptLook.of(receipt("a", mapOf("resolution" to r.wire))))
        }
        assertEquals(InterruptLook.Pending, InterruptLook.of(receipt("a", mapOf("resolution" to "pending"))))
    }

    @Test fun aBatchIsDrawnOnceOnItsHead() {
        val batch = buildJsonObject {
            put("cause", "model_error"); put("machine", "mlx"); put("total", 2); put("pending", 1); put("resolved", 1)
            put("tasks", buildJsonArray {
                add(buildJsonObject { put("receipt", "h"); put("todo_id", "todo_h"); put("title", "One"); put("resolution", "cancelled") })
                add(buildJsonObject { put("receipt", "m"); put("todo_id", "todo_m"); put("title", "Two"); put("resolution", "pending") })
            })
        }
        val head = InterruptLook.of(receipt("h", mapOf("resolution" to "cancelled"), batch)) as InterruptLook.Batch
        assertEquals(2, head.batch.total)
        assertEquals(1, head.batch.pending)
        assertEquals("mlx", head.batch.machine)
        assertEquals(listOf(InterruptResolution.Cancelled, InterruptResolution.Pending), head.batch.tasks.map { it.resolution })
        assertEquals(InterruptLook.Hidden, InterruptLook.of(receipt("m", mapOf("resolution" to "pending", "group" to "h"))))
    }
}
