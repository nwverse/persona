package dev.persona.watch.data

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class LegacyScreenTest {
    @Test fun numberedTextIsNeverInferredAsTerminalOptionsForAnotherKind() {
        for (kind in listOf("permission", "question", "plan", "future")) {
            for (structured in listOf(false, true)) for (requiresInput in listOf(false, true)) {
                val row = JSONObject().put("id", "sample").put("kind", kind).put("screen", "1. Allow\n2. Deny")
                    .put("secret", requiresInput)
                if (structured) row.put("dialog", JSONObject("""{"options":[{"key":"1","label":"Allow"}],"multi":true}"""))
                val body = JSONObject().put("interactions", org.json.JSONArray().put(row))
                val interaction = WatchParser.interactions(body, emptyList()).single()
                assertTrue(interaction.options.isEmpty())
                assertFalse(interaction.rawOptions)
                assertFalse(interaction.multiSelect)
                assertEquals(requiresInput, interaction.requiresInput)
                assertThrows(HubException::class.java) { WatchResolution.validate(interaction, JSONObject("""{"keys":["1"]}""")) }
            }
        }
    }
    @Test fun legacyFlagRequiresActualScreenOptionsAndStructuredScreenStaysStructured() {
        fun parse(row: String) = WatchParser.interactions(JSONObject("""{"interactions":[$row]}"""), emptyList()).single()
        val legacy = parse("""{"id":"s","kind":"screen","screen":"1. First\n2. Second"}""")
        assertTrue(legacy.rawOptions)
        assertEquals(listOf("1", "2"), legacy.options.map { it.key })
        assertFalse(parse("""{"id":"s","kind":"screen","screen":"No numbered options"}""").rawOptions)
        val structured = parse("""{"id":"s","kind":"screen","screen":"1. Old","dialog":{"options":[{"key":"new","label":"New"}]}}""")
        assertFalse(structured.rawOptions)
        assertEquals(listOf("new"), structured.options.map { it.key })
    }
    @Test fun protectedScreenPayloadNeverOffersTerminalApprovalKeys() {
        val protectedPayloads = listOf(JSONObject().put("secret", true),
            JSONObject().put("fields", org.json.JSONArray().put(JSONObject().put("name", "secret"))),
            JSONObject().put("tool_name", "secret_run"))
        for (protection in protectedPayloads) {
            for (structured in listOf(false, true)) {
                val row = JSONObject(protection.toString()).put("id", "s").put("kind", "screen").put("screen", "1. Allow\n2. Deny")
                if (structured) row.put("dialog", JSONObject().put("options",
                    org.json.JSONArray().put(JSONObject().put("key", "1").put("label", "Allow"))).put("multi", true))
                val body = JSONObject().put("interactions", org.json.JSONArray().put(row))
                val interaction = WatchParser.interactions(body, emptyList()).single()
                assertTrue(interaction.requiresInput)
                assertFalse(interaction.canAnswer)
                assertFalse(interaction.rawOptions)
                assertTrue(interaction.options.isEmpty())
                for (key in listOf("1", "enter", "esc")) {
                    assertEquals("read_only", assertThrows(HubException::class.java) {
                        WatchResolution.validate(interaction, JSONObject().put("keys", org.json.JSONArray().put(key)))
                    }.code)
                }
            }
        }
    }
    @Test fun onlyTheLastContiguousNumberedBlockIsSelectable() {
        val options = WatchParser.rawScreenOptions("1. Older\n2. Old option\n---\n❯ 1. Current\n2. Another")
        assertEquals(listOf("Current", "Another"), options.map { it.label })
        assertTrue(options.first().checked)
        assertTrue(WatchParser.rawScreenOptions("1. One\n3. Gap").isEmpty())
    }
    @Test fun numericLegacyChoiceUsesKeysWhileStructuredChoiceUsesOption() {
        val base = WatchInteraction("sample", "computer", "session", "screen", "Sample", "Choose",
            options = listOf(DialogOption("1", "First")), canAnswer = true)
        val legacy = base.copy(rawOptions = true)
        val legacyAnswer = WatchResolution.screen(legacy, setOf("1"))
        assertEquals("1", legacyAnswer.getJSONArray("keys").getString(0))
        WatchResolution.validate(legacy, legacyAnswer)
        assertEquals("1", WatchResolution.screen(base, setOf("1")).getString("option"))
        try { WatchResolution.validate(base, legacyAnswer); fail("A structured dialog must not accept inferred numeric keys") } catch (_: HubException) { }
    }
    @Test fun sourceMetadataNeverBecomesTheWatchOwnersBubble() {
        val body = JSONObject("""{"items":[{"id":"sample","kind":"text","role":"user","text":"Sample relay","source":{"kind":"conversation","title":"Another conversation"}}]}""")
        val message = WatchParser.messages(body).single()
        assertFalse(message.own)
        assertEquals("Another conversation", message.sourceTitle)
        assertEquals("conversation", message.sourceKind)
    }
}
