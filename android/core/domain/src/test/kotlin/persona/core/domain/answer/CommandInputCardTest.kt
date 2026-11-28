package persona.core.domain.answer

import persona.core.contracts.Interaction
import persona.core.contracts.JsonCodec
import persona.core.contracts.Resolution
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A command waiting for input (#14527): a payload-only card (a node's `tool_name: command_input` and `tool_input`,
 * before the manager adds its one field) still sends its line as the `input` field, once, from the conversation.
 */
class CommandInputCardTest {
    private fun card(fields: String? = null) = JsonCodec.decodeFromString<Interaction>(
        """{"id":"ci_1","machine_id":"m","session_id":"s","kind":"permission","tool_name":"command_input",
            "tool_input":{"command":"example-cli login","prompt":"Enter the code:"}""" +
            (fields?.let { ""","fields":$it""" } ?: "") + "}")

    private val line = mapOf("input" to "test-code-123")

    @Test fun aPayloadOnlyCardSendsItsLine() {
        val request = card()
        assertTrue(request.isCommandInput)
        assertFalse(request.hasFieldsForm)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow).allowed)
        assertEquals(Resolution(allow = true, values = line),
            AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = line)).resolution)
    }

    @Test fun theLineGoesOnlyWithAllowInTheConversation() {
        val request = card()
        assertFalse(AnswerBuilder.build(request, AnswerAction.Deny, AnswerDraft(values = line)).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Always, AnswerDraft(values = line)).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Always).allowed)
        for (entry in AnswerEntry.entries.filter { it != AnswerEntry.Conversation }) {
            assertFalse("$entry", AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = line),
                AnswerContext(entry = entry)).allowed)
        }
        // A value under another key is not a line.
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = mapOf("other" to "x"))).allowed)
    }

    @Test fun aCardWithTheManagersFieldSendsItToo() {
        val request = card("""[{"key":"input","label":"Enter the code:","secret":true}]""")
        assertTrue(request.hasFieldsForm)
        assertEquals(Resolution(allow = true, values = line),
            AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = line)).resolution)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = mapOf("input" to ""))).allowed)
    }
}
