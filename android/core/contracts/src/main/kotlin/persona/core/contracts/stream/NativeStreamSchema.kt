package persona.core.contracts.stream

import java.math.BigDecimal
import kotlinx.serialization.json.*

/** Raw validation before generated decoders can fill defaults or round numbers. No executable client runtime. */
object NativeStreamSchema {
    const val FrameBytes = 1024 * 1024
    const val RequestBytes = 64 * 1024
    private val schemas = mutableMapOf<String, JsonObject>()
    private val json = Json { ignoreUnknownKeys = false }

    @Synchronized private fun schema(file: String): JsonObject = schemas.getOrPut(file) {
        val stream = checkNotNull(javaClass.getResourceAsStream("/extension-schema/$file"))
        stream.bufferedReader().use { json.parseToJsonElement(it.readText()).jsonObject }
    }

    fun parse(text: String, definition: String, bytes: Int = FrameBytes): JsonObject {
        require(text.length <= bytes && text.toByteArray(Charsets.UTF_8).size <= bytes)
        // Bound nesting before the recursive JSON decoder, including malformed/unknown fields.
        var depth = 0
        var quoted = false
        var escaped = false
        var start = 0
        val containers = ArrayDeque<MutableSet<String>?>()
        for ((index, char) in text.withIndex()) {
            if (quoted) {
                if (escaped) escaped = false else if (char == '\\') escaped = true else if (char == '"') {
                    quoted = false
                    var following = index + 1
                    while (following < text.length && text[following].isWhitespace()) following++
                    if (following < text.length && text[following] == ':') {
                        val key = json.decodeFromString<String>(text.substring(start, index + 1))
                        require(containers.lastOrNull()?.add(key) == true)
                    }
                }
            } else when (char) {
                '"' -> { quoted = true; start = index }
                '{', '[' -> { depth++; require(depth <= 16); containers.addLast(if (char == '{') mutableSetOf() else null) }
                '}', ']' -> { depth--; require(depth >= 0); containers.removeLast() }
            }
        }
        require(!quoted && depth == 0)
        val raw = json.parseToJsonElement(text).jsonObject
        validate(definition, raw)
        return raw
    }

    fun validate(definition: String, value: JsonElement) {
        check(value, schema("stream.schema.json").getValue("\$defs").jsonObject.getValue(definition).jsonObject,
            "stream.schema.json")
    }

    private fun check(value: JsonElement, rule: JsonObject, file: String) {
        rule["\$ref"]?.jsonPrimitive?.content?.let { ref ->
            val target = ref.substringBefore('#').ifEmpty { file }
            val name = ref.substringAfter("#/$" + "defs/")
            check(value, schema(target).getValue("\$defs").jsonObject.getValue(name).jsonObject, target)
        }
        rule["oneOf"]?.jsonArray?.let { choices ->
            val discriminator = rule["x-discriminator"]?.jsonPrimitive?.content
            val narrowed = if (discriminator != null && value is JsonObject) choices.filter { choice ->
                val branch = choice.jsonObject
                val ref = branch["\$ref"]?.jsonPrimitive?.content
                val resolved = if (ref == null) branch else {
                    val target = ref.substringBefore('#').ifEmpty { file }
                    schema(target).getValue("\$defs").jsonObject.getValue(ref.substringAfter("#/$" + "defs/")).jsonObject
                }
                val constant = resolved["properties"]?.jsonObject?.get(discriminator)?.jsonObject?.get("const")
                constant == null || constant == value[discriminator]
            } else choices
            require(narrowed.count { runCatching { check(value, it.jsonObject, file) }.isSuccess } == 1)
        }
        rule["const"]?.let { require(value == it) }
        rule["enum"]?.jsonArray?.let { require(value in it) }
        val type = rule["type"]?.jsonPrimitive?.content
        when (type) {
            "object" -> require(value is JsonObject)
            "array" -> require(value is JsonArray)
            "string" -> require(value is JsonPrimitive && value.isString)
            "boolean" -> require(value is JsonPrimitive && !value.isString && value.booleanOrNull != null)
            "null" -> require(value is JsonNull)
            "number", "integer" -> {
                require(value is JsonPrimitive && !value.isString)
                val number = BigDecimal(value.content)
                if (type == "integer") require(number.stripTrailingZeros().scale() <= 0)
                rule["minimum"]?.let { require(number >= BigDecimal(it.jsonPrimitive.content)) }
                rule["maximum"]?.let { require(number <= BigDecimal(it.jsonPrimitive.content)) }
            }
        }
        if (value is JsonPrimitive && value.isString) {
            val length = value.content.codePointCount(0, value.content.length)
            rule["minLength"]?.let { require(length >= it.jsonPrimitive.int) }
            rule["maxLength"]?.let { require(length <= it.jsonPrimitive.int) }
            rule["pattern"]?.let { require(Regex(it.jsonPrimitive.content).containsMatchIn(value.content)) }
        }
        if (value is JsonArray) {
            rule["minItems"]?.let { require(value.size >= it.jsonPrimitive.int) }
            rule["maxItems"]?.let { require(value.size <= it.jsonPrimitive.int) }
            if (rule["uniqueItems"] == JsonPrimitive(true)) require(value.toSet().size == value.size)
            rule["items"]?.jsonObject?.let { item -> value.forEach { check(it, item, file) } }
        }
        if (value is JsonObject) {
            rule["required"]?.jsonArray?.forEach { require(it.jsonPrimitive.content in value) }
            rule["minProperties"]?.let { require(value.size >= it.jsonPrimitive.int) }
            rule["maxProperties"]?.let { require(value.size <= it.jsonPrimitive.int) }
            val properties = rule["properties"]?.jsonObject.orEmpty()
            properties.forEach { (key, child) -> value[key]?.let { check(it, child.jsonObject, file) } }
            val additional = rule["additionalProperties"]
            // Stream native objects are closed even where legacy snapshot schemas were permissive.
            if (type == "object" && "properties" in rule && additional == null) require(value.keys.all { it in properties })
            value.filterKeys { it !in properties }.forEach { (key, child) ->
                require(additional != JsonPrimitive(false))
                if (additional is JsonObject) check(child, additional, file)
                rule["propertyNames"]?.let { check(JsonPrimitive(key), it.jsonObject, file) }
            }
        }
    }
}
