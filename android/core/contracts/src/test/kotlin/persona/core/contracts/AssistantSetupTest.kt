package persona.core.contracts

import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.encodeToJsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantConfigUpdate

class AssistantSetupTest {
    @Test fun recentTaskChatsIsOptionalAndWritesOnlyItself() {
        assertNull(JsonCodec.decodeFromJsonElement<AssistantConfig>(buildJsonObject { }).recentTaskChats)
        val read = JsonCodec.decodeFromJsonElement<AssistantConfig>(buildJsonObject { put("recent_task_chats", true) })
        assertEquals(true, read.recentTaskChats)
        val written = JsonCodec.encodeToJsonElement(AssistantConfigUpdate(recentTaskChats = true)).jsonObject
        assertEquals(setOf("recent_task_chats"), written.keys)
        assertEquals(true, written.getValue("recent_task_chats").jsonPrimitive.boolean)
    }

    @Test fun conversationOrderDistinguishesUnchangedResetAndSavedIds() {
        assertNull(JsonCodec.decodeFromJsonElement<AssistantConfig>(buildJsonObject { }).conversationOrder)
        val read = JsonCodec.decodeFromJsonElement<AssistantConfig>(
            JsonCodec.parseToJsonElement("""{"conversation_order":["b","unknown","a"]}"""),
        )
        assertEquals(listOf("b", "unknown", "a"), read.conversationOrder)
        assertTrue("null is omitted and leaves the setting unchanged",
            "conversation_order" !in JsonCodec.encodeToJsonElement(AssistantConfigUpdate()).jsonObject)
        val reset = JsonCodec.encodeToJsonElement(AssistantConfigUpdate(conversationOrder = emptyList())).jsonObject
        assertEquals(setOf("conversation_order"), reset.keys)
        assertEquals(emptyList<Nothing>(), reset.getValue("conversation_order").jsonArray)
        val written = JsonCodec.encodeToJsonElement(
            AssistantConfigUpdate(conversationOrder = listOf("b", "unknown", "a")),
        ).jsonObject.getValue("conversation_order").jsonArray
        assertEquals(listOf("b", "unknown", "a"), written.map { it.jsonPrimitive.content })
    }
}
