package persona.feature.settings

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

internal enum class KnowledgeSelectionPart(val wire: String, val knownRules: List<String>) {
    Messages("messages", listOf("mentions", "mine", "dm", "group", "bot", "official", "muted", "collapsed", "done")),
    Documents("docs", listOf("mentions", "comment_mentions", "authored", "viewed")),
}

internal data class KnowledgeItemId(val source: String, val kind: String, val id: String)

internal enum class KnowledgeItemMode { Skip, Rules, All }

internal class KnowledgeSelectionItem(
    val id: KnowledgeItemId,
    val name: String,
    val mode: KnowledgeItemMode,
    val days: Int?,
    private val original: JsonObject = JsonObject(emptyMap()),
) {
    val read: Boolean get() = mode != KnowledgeItemMode.Skip
    val store: Boolean get() = mode == KnowledgeItemMode.All

    val raw: JsonObject
        get() {
            val fields = original.toMutableMap()
            fields["source"] = JsonPrimitive(id.source)
            fields["kind"] = JsonPrimitive(id.kind)
            fields["id"] = JsonPrimitive(id.id)
            if (name.isBlank()) fields.remove("name") else fields["name"] = JsonPrimitive(name)
            fields["read"] = JsonPrimitive(read)
            fields["store"] = JsonPrimitive(store)
            fields["days"] = if (mode == KnowledgeItemMode.All && days != null) JsonPrimitive(days) else JsonNull
            return JsonObject(fields)
        }

    fun copy(
        id: KnowledgeItemId = this.id,
        name: String = this.name,
        mode: KnowledgeItemMode = this.mode,
        days: Int? = this.days,
    ) = KnowledgeSelectionItem(id, name, mode, days, original)

    override fun equals(other: Any?): Boolean = other is KnowledgeSelectionItem && raw == other.raw
    override fun hashCode(): Int = raw.hashCode()

    companion object {
        fun newChat(source: String, id: String, name: String) = KnowledgeSelectionItem(
            KnowledgeItemId(source, "chat", id), name, KnowledgeItemMode.Rules, null,
        )

        fun newDocument(source: String, id: String, name: String) = KnowledgeSelectionItem(
            KnowledgeItemId(source, "doc", id), name, KnowledgeItemMode.Rules, null,
        )

        fun fromRaw(raw: JsonObject): KnowledgeSelectionItem {
            val read = raw["read"]?.jsonPrimitive?.booleanOrNull == true
            val store = raw["store"]?.jsonPrimitive?.booleanOrNull == true
            val mode = when {
                !read -> KnowledgeItemMode.Skip
                store -> KnowledgeItemMode.All
                else -> KnowledgeItemMode.Rules
            }
            return KnowledgeSelectionItem(
                id = KnowledgeItemId(
                    raw.getValue("source").jsonPrimitive.content,
                    raw.getValue("kind").jsonPrimitive.content,
                    raw.getValue("id").jsonPrimitive.content,
                ),
                name = raw["name"]?.jsonPrimitive?.content.orEmpty(),
                mode = mode,
                days = if (mode == KnowledgeItemMode.All) raw["days"].nullableInt() else null,
                original = raw,
            )
        }
    }
}

internal data class KnowledgeSelectionRule(val raw: JsonObject) {
    val on: Boolean get() = raw["on"]?.jsonPrimitive?.booleanOrNull == true
    val days: Int? get() = raw["days"].nullableInt()
    val attachmentDays: Int? get() = raw["attachment_days"].nullableInt()
    val includeAll: Boolean? get() = raw["include_all"]?.jsonPrimitive?.booleanOrNull
    val context: Int? get() = raw["context"].nullableInt()
}

/**
 * The complete selection document. Edits rebuild only the JSON objects on their path,
 * so fields and rules introduced by a newer hub survive an older client's whole-document PUT.
 */
internal class KnowledgeSelectionDocument(val root: JsonObject) {
    init {
        validateShape()
    }

    fun encoded(): String = root.toString()

    fun order(part: KnowledgeSelectionPart): List<List<String>> =
        root.getValue(part.wire).jsonObject.getValue("order").jsonArray.map { level ->
            level.jsonArray.map { it.jsonPrimitive.content }
        }

    fun rule(part: KnowledgeSelectionPart, name: String): KnowledgeSelectionRule? =
        root.getValue(part.wire).jsonObject.getValue("rules").jsonObject[name]
            ?.let { KnowledgeSelectionRule(it.jsonObject) }

    fun items(): List<KnowledgeSelectionItem> =
        root["items"]?.jsonArray.orEmpty().map { KnowledgeSelectionItem.fromRaw(it.jsonObject) }

    fun item(id: KnowledgeItemId): KnowledgeSelectionItem? = items().firstOrNull { it.id == id }

    fun setRuleOn(part: KnowledgeSelectionPart, name: String, on: Boolean) =
        withRule(part, name) { JsonObject(it + ("on" to JsonPrimitive(on))) }

    fun setRuleDays(part: KnowledgeSelectionPart, name: String, days: Int?): KnowledgeSelectionDocument {
        requireDays(days)
        return withRule(part, name) { JsonObject(it + ("days" to days.jsonOrNull())) }
    }

    fun setAttachmentDays(name: String, days: Int?): KnowledgeSelectionDocument {
        requireDays(days)
        return withRule(KnowledgeSelectionPart.Messages, name) {
            JsonObject(it + ("attachment_days" to days.jsonOrNull()))
        }
    }

    fun setIncludeAll(includeAll: Boolean) =
        withRule(KnowledgeSelectionPart.Messages, "mentions") {
            JsonObject(it + ("include_all" to JsonPrimitive(includeAll)))
        }

    fun setMineContext(context: Int?): KnowledgeSelectionDocument {
        require(context == null || context in 0..200) { "context must be from 0 to 200" }
        return withRule(KnowledgeSelectionPart.Messages, "mine") {
            JsonObject(it + ("context" to context.jsonOrNull()))
        }
    }

    fun moveLevel(part: KnowledgeSelectionPart, name: String, delta: Int): KnowledgeSelectionDocument {
        val levels = order(part).toMutableList()
        val index = levels.indexOfFirst { name in it }
        if (index < 0) return this
        val destination = index + delta
        if (destination !in levels.indices) return this
        val level = levels[index]
        levels[index] = levels[destination]
        levels[destination] = level
        return withOrder(part, levels)
    }

    fun regroup(part: KnowledgeSelectionPart, name: String, joinPrevious: Boolean): KnowledgeSelectionDocument {
        val levels = order(part).toMutableList()
        val index = levels.indexOfFirst { name in it }
        if (index < 0 || (joinPrevious && index == 0) || (!joinPrevious && levels[index].size == 1)) return this
        val remaining = levels[index].filterNot { it == name }
        if (joinPrevious) {
            levels[index - 1] = levels[index - 1] + name
            if (remaining.isEmpty()) levels.removeAt(index) else levels[index] = remaining
        } else {
            levels[index] = remaining
            levels.add(index + 1, listOf(name))
        }
        return withOrder(part, levels)
    }

    fun setItem(item: KnowledgeSelectionItem): KnowledgeSelectionDocument {
        if (item.mode == KnowledgeItemMode.All) requireDays(item.days)
        val updated = items().toMutableList()
        val index = updated.indexOfFirst { it.id == item.id }
        if (index < 0) updated += item else updated[index] = item
        return withItems(updated)
    }

    fun removeItem(id: KnowledgeItemId): KnowledgeSelectionDocument {
        val updated = items().filterNot { it.id == id }
        return if (updated.size == items().size) this else withItems(updated)
    }

    private fun withRule(
        part: KnowledgeSelectionPart,
        name: String,
        transform: (JsonObject) -> JsonObject,
    ): KnowledgeSelectionDocument {
        val group = root.getValue(part.wire).jsonObject
        val rules = group.getValue("rules").jsonObject
        val current = rules.getValue(name).jsonObject
        val changed = transform(current)
        if (changed == current) return this
        return KnowledgeSelectionDocument(
            JsonObject(root + (part.wire to JsonObject(group + ("rules" to JsonObject(rules + (name to changed)))))),
        )
    }

    private fun withOrder(part: KnowledgeSelectionPart, levels: List<List<String>>): KnowledgeSelectionDocument {
        val group = root.getValue(part.wire).jsonObject
        val wireOrder = JsonArray(levels.map { level -> JsonArray(level.map(::JsonPrimitive)) })
        return KnowledgeSelectionDocument(JsonObject(root + (part.wire to JsonObject(group + ("order" to wireOrder)))))
    }

    private fun withItems(items: List<KnowledgeSelectionItem>): KnowledgeSelectionDocument {
        val changed = JsonObject(root + ("items" to JsonArray(items.map { it.raw })))
        return if (changed == root) this else KnowledgeSelectionDocument(changed)
    }

    private fun validateShape() {
        require(root["version"]?.jsonPrimitive?.intOrNull == 1) { "unsupported selection version" }
        for (part in KnowledgeSelectionPart.entries) {
            val group = root.getValue(part.wire).jsonObject
            val rules = group.getValue("rules").jsonObject
            require(rules.keys.containsAll(part.knownRules)) { "${part.wire} is missing a rule" }
            val flattened = order(part).flatten()
            require(order(part).all { it.isNotEmpty() }) { "${part.wire} has an empty priority" }
            require(flattened.size == flattened.distinct().size && flattened.toSet() == rules.keys) {
                "${part.wire} order must name every rule exactly once"
            }
            for (name in part.knownRules) {
                val rule = rules.getValue(name).jsonObject
                require(rule["on"]?.jsonPrimitive?.booleanOrNull != null) { "${part.wire}.$name.on is required" }
                requireDays(rule["days"].nullableInt())
                if (part == KnowledgeSelectionPart.Messages) {
                    requireDays(rule["attachment_days"].nullableInt())
                    if (name != "mentions") require("include_all" !in rule)
                    if (name != "mine") require("context" !in rule)
                    if (name == "mine") rule["context"]?.nullableInt()?.let { require(it in 0..200) }
                } else {
                    require("include_all" !in rule && "context" !in rule && "attachment_days" !in rule)
                }
            }
        }
        val seen = mutableSetOf<KnowledgeItemId>()
        root["items"]?.jsonArray.orEmpty().forEach { element ->
            val item = KnowledgeSelectionItem.fromRaw(element.jsonObject)
            require(item.id.source.isNotEmpty() && item.id.id.isNotEmpty())
            require(item.id.kind == "chat" || item.id.kind == "doc")
            require(seen.add(item.id)) { "duplicate selection item" }
            val raw = element.jsonObject
            require(raw["read"]?.jsonPrimitive?.booleanOrNull != null && raw["store"]?.jsonPrimitive?.booleanOrNull != null)
            require(item.read || !item.store) { "store needs read" }
            requireDays(raw["days"].nullableInt())
        }
        root["history"]?.jsonObject?.forEach { (source, history) ->
            require(source.isNotEmpty())
            requireDays(history.jsonObject["back_days"].nullableInt())
        }
    }

    companion object {
        fun parse(raw: String): KnowledgeSelectionDocument =
            KnowledgeSelectionDocument(Json.parseToJsonElement(raw).jsonObject)

        fun defaults(): KnowledgeSelectionDocument {
            fun messageRule(on: Boolean, includeAll: Boolean? = null, context: Int? = null) = buildJsonObject {
                put("on", on)
                put("days", JsonNull)
                put("attachment_days", JsonNull)
                includeAll?.let { put("include_all", it) }
                context?.let { put("context", it) }
            }
            fun documentRule() = buildJsonObject {
                put("on", true)
                put("days", JsonNull)
            }
            val messageRules = KnowledgeSelectionPart.Messages.knownRules.associateWith { name ->
                when (name) {
                    "mentions" -> messageRule(true, includeAll = true)
                    "mine" -> messageRule(true, context = 50)
                    "dm" -> messageRule(true)
                    else -> messageRule(false)
                }
            }
            val messages = buildJsonObject {
                put("order", JsonArray(listOf(
                    listOf("mentions", "mine"), listOf("dm"), listOf("group"), listOf("bot"),
                    listOf("official"), listOf("muted"), listOf("collapsed"), listOf("done"),
                ).map { level -> JsonArray(level.map(::JsonPrimitive)) }))
                put("rules", JsonObject(messageRules))
            }
            val documents = buildJsonObject {
                put("order", JsonArray(listOf(
                    listOf("mentions", "comment_mentions"), listOf("authored"), listOf("viewed"),
                ).map { level -> JsonArray(level.map(::JsonPrimitive)) }))
                put("rules", JsonObject(KnowledgeSelectionPart.Documents.knownRules.associateWith { documentRule() }))
            }
            return KnowledgeSelectionDocument(buildJsonObject {
                put("version", 1)
                put("messages", messages)
                put("docs", documents)
                put("items", JsonArray(emptyList()))
                put("history", JsonObject(emptyMap()))
            })
        }
    }
}

private fun JsonElement?.nullableInt(): Int? =
    if (this == null || this == JsonNull) null else jsonPrimitive.int

private fun Int?.jsonOrNull(): JsonElement = this?.let(::JsonPrimitive) ?: JsonNull

private fun requireDays(days: Int?) {
    require(days == null || days in 1..36500) { "days must be from 1 to 36500 or null" }
}
