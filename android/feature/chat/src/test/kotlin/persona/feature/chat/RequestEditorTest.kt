package persona.feature.chat

import persona.core.contracts.*
import persona.core.domain.answer.*
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

class RequestEditorTest {
    @Test fun requestDraftSurvivesReopeningAndSharesOriginalLabelsButNeverSecretsOrReplacedPrompts() {
        val disk = mutableMapOf<String, String>()
        fun journal() = RequestDraftJournal(disk::get) { key, value -> if (value == null) disk.remove(key) else disk[key] = value }
        val question = Question("选择方案", multiSelect = true, options = listOf(QuestionOption("保留 (Recommended)")))
        val request = Interaction("request", machineId = "machine", sessionId = "session", kind = "question", questions = listOf(question))
        val draft = AnswerDraft(picks = mapOf(question.question to setOf("保留 (Recommended)")), other = mapOf("补充" to "稍后再删"), page = 1)
        journal().save("pairing-a", request, draft)
        assertEquals(draft, journal().load("pairing-a", request))
        assertEquals(draft, journal().load("pairing-a", request.copy(expiresAt = "2026-12-01T00:00:00Z")))
        assertEquals(AnswerDraft(), journal().load("pairing-b", request))
        assertEquals(AnswerDraft(), journal().load("pairing-a", request.copy(questions = listOf(question.copy(question = "新问题")))))
        journal().save("pairing-a", request.copy(secret = true), draft)
        assertTrue(disk.isEmpty())
        val form = request.copy(kind = "elicitation", elicitation = Elicitation(requestedSchema = JsonCodec.parseToJsonElement(
            """{"type":"object","properties":{"note":{"type":"string"},"password":{"type":"string","format":"password"}}}""").jsonObject))
        journal().save("pairing-a", form, AnswerDraft(fields = buildJsonObject { put("note", "继续"); put("password", "private-value") }))
        assertEquals(buildJsonObject { put("note", "继续") }, journal().load("pairing-a", form).fields)
        assertFalse(disk.values.single().contains("private-value"))
        journal().clear("pairing-a", form)
        assertTrue(disk.isEmpty())
        val url = request.copy(kind = "elicitation", elicitation = Elicitation(mode = "url", url = "https://example.com/authorize"))
        journal().save("pairing-a", url, AnswerDraft(openedElicitationUrl = url.elicitation!!.url))
        assertNull(journal().load("pairing-a", url).openedElicitationUrl)
        assertTrue(disk.isEmpty())
    }

    /** A password card's typed password (#1581) never reaches the saved request drafts, alone or beside other draft text. */
    @Test fun aPasswordCardsTypedPasswordIsNeverJournaled() {
        val disk = mutableMapOf<String, String>()
        val journal = RequestDraftJournal(disk::get) { key, value -> if (value == null) disk.remove(key) else disk[key] = value }
        val card = Interaction("ix_secret", machineId = "hub", sessionId = "chat", kind = "permission", toolName = "secret_run",
            fields = listOf(InteractionField(key = "password", label = "Password", secret = true)))
        journal.save("pairing-a", card, AnswerDraft(password = "private-value"))
        assertTrue(disk.isEmpty())
        journal.save("pairing-a", card, AnswerDraft(password = "private-value").copy(message = "note"))
        assertFalse(disk.values.single().contains("private-value"))
        assertEquals("", journal.load("pairing-a", card).password)
    }

    @Test fun elicitationValidatesTypedFieldsAndAllOutcomesWhileSourcesAndCommandsRequireEvidence() {
        val request = JsonCodec.decodeFromString(Interaction.serializer(), """{
            "id":"form","kind":"elicitation","mcp_server_name":"example-service","agent_type":"Explore",
            "elicitation":{"mode":"form","requested_schema":{"type":"object","required":["count","enabled","choice","tags","note"],"properties":{
                "count":{"type":"integer","minimum":1,"maximum":3},"enabled":{"type":"boolean"},
                "choice":{"type":"string","enum":["a","b"]},"tags":{"type":"array","items":{"type":"string","enum":["x","y"]},"minItems":1},
                "note":{"type":"string","minLength":2,"maxLength":5}
            }}}} """)
        val fields = buildJsonObject {
            put("count", "2"); put("enabled", false); put("choice", "a"); put("tags", JsonArray(listOf(JsonPrimitive("x")))); put("note", "继续")
        }
        val draft = AnswerDraft(fields = fields)
        val accepted = AnswerBuilder.build(request, AnswerAction.SubmitElicitation, draft).resolution!!
        assertEquals("accept", accepted.action)
        assertEquals(JsonPrimitive(2), accepted.content?.get("count"))
        assertEquals(JsonPrimitive(false), accepted.content?.get("enabled"))
        assertEquals("decline", AnswerBuilder.build(request, AnswerAction.DeclineElicitation).resolution?.action)
        assertEquals("cancel", AnswerBuilder.build(request, AnswerAction.CancelElicitation).resolution?.action)
        assertFalse(AnswerBuilder.build(request, AnswerAction.SubmitElicitation, draft, AnswerContext(locked = true)).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.SubmitElicitation, draft, AnswerContext(entry = AnswerEntry.Notification)).allowed)
        listOf(JsonObject(fields - "enabled"), JsonObject(fields + ("count" to JsonPrimitive("4"))),
            JsonObject(fields + ("tags" to JsonArray(listOf(JsonPrimitive("other")))))).forEach {
            assertFalse(AnswerBuilder.build(request, AnswerAction.SubmitElicitation, draft.copy(fields = it)).allowed)
        }
        val unsupported = request.copy(elicitation = request.elicitation!!.copy(requestedSchema = JsonObject(
            request.elicitation!!.requestedSchema!! + ("oneOf" to JsonArray(emptyList())))))
        assertFalse(AnswerBuilder.build(unsupported, AnswerAction.SubmitElicitation, draft).allowed)
        assertEquals("来自子 Agent · Explore\n来自 MCP 服务器 example-service", request.requestSourceLabel()?.shown)
        assertNull(Interaction("unknown", toolName = "mcp__untrusted__tool").requestSourceLabel())
        val screen = Interaction("screen-request", kind = "screen", toolUseId = "tool-id")
        val tool = Item("item", kind = "tool_use", toolUseId = "tool-id", toolInput = buildJsonObject { put("command", "printf '完整命令\\n'\nprintf '第二行'") })
        assertEquals(RequestCommand("screen-request", "tool-id", tool.toolInput!!.jsonObject["command"]!!.jsonPrimitive.content), screen.requestCommand(listOf(tool)))
        assertNull(screen.requestCommand(listOf(tool.copy(toolUseId = "other"))))
        assertNull(screen.copy(toolUseId = null).requestCommand(listOf(tool)))
        assertNull(screen.copy(secret = true).requestCommand(listOf(tool)))
    }
}
