package persona.core.domain.views

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

// The hub's trailing tag on a card's answer is read only when it is exactly that (docs/assistant/assistant-views.md "Persistent submission").
class PersonaActionTagTest {
    @Test fun theHubsTag() {
        val text = "选 B：队列去重\n优先级：高\n<persona-action view=\"item#0/1\" action=\"choose-b\">{\"values\":{\"priority\":\"high\",\"note\":\"a\\u003cb\"}}</persona-action>"
        val (words, tag) = PersonaActionTag.split(text)!!
        assertEquals("选 B：队列去重\n优先级：高", words)
        assertEquals("item#0/1", tag.view)
        assertEquals("choose-b", tag.action)
        assertEquals(mapOf("priority" to "high", "note" to "a<b"), tag.values)
        assertEquals("a&b", PersonaActionTag.split("x\n<persona-action view=\"a&amp;b\" action=\"c\">{\"values\":{}}</persona-action>")!!.second.view)
    }

    @Test fun anythingElseIsWhatTheUserWrote() {
        listOf(
            "<persona-action view=\"v\" action=\"a\">{\"values\":{}}</persona-action> 然后我又写了一句",
            "x\n<persona-action action=\"a\" view=\"v\">{\"values\":{}}</persona-action>",
            "x\n<persona-action view=\"v\" action=\"a\" extra=\"1\">{\"values\":{}}</persona-action>",
            "x\n<persona-action view=\"v\" action=\"a\">{\"values\":{},\"more\":1}</persona-action>",
            "x\n<persona-action view=\"v\" action=\"a\">{\"values\":{\"n\":1}}</persona-action>",
            "x\n<persona-action view=\"v\" action=\"a\">not json</persona-action>",
            "x\n<persona-action view=\"\" action=\"a\">{\"values\":{}}</persona-action>",
            "x\n<persona-action view=\"v\" action=\"a\">{\"values\":{\"n\":\"<b>\"}}</persona-action>",
        ).forEach { assertNull(it, PersonaActionTag.split(it)) }
    }
}
