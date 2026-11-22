package persona.core.domain.views

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import persona.core.contracts.JsonCodec
import persona.core.domain.decodeXmlEntities

/**
 * The tag the hub ends a card's answer with (docs/assistant/assistant-views.md "Persistent submission"):
 * `\n<persona-action view="…" action="…">{"values":{…}}</persona-action>`. Only that exact tag at the very end is
 * read (the attributes XML-escaped, the JSON one object of string values): text that merely looks like one, or one
 * anywhere else, is what the user wrote and stays.
 */
data class PersonaActionTag(val view: String, val action: String, val values: Map<String, String>) {
    companion object {
        private val tag = Regex("""\n?<persona-action view="([^"<>]*)" action="([^"<>]*)">(\{[^<>]*\})</persona-action>\s*\z""")

        /** The words before the tag and the tag, or null when [text] does not end with one. */
        fun split(text: String): Pair<String, PersonaActionTag>? {
            val match = tag.find(text) ?: return null
            val view = decodeXmlEntities(match.groupValues[1])
            val action = decodeXmlEntities(match.groupValues[2])
            if (view.isEmpty() || action.isEmpty()) return null
            val payload = runCatching { JsonCodec.parseToJsonElement(match.groupValues[3]) }.getOrNull() as? JsonObject ?: return null
            if (payload.keys != setOf("values")) return null
            val values = payload["values"] as? JsonObject ?: return null
            val strings = values.mapValues { (_, v) -> (v as? JsonPrimitive)?.takeIf { it.isString }?.content ?: return null }
            return text.substring(0, match.range.first).trimEnd() to PersonaActionTag(view, action, strings)
        }
    }
}
