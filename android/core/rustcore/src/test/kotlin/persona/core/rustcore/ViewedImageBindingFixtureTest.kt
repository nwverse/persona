package persona.core.rustcore

import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/** Real Kotlin/JNI viewed-image leaf replay, with expected values from the shared corpus and checked-in iOS overlay. */
class ViewedImageBindingFixtureTest {
    private val root: File = generateSequence(File(".").canonicalFile) { it.parentFile }
        .firstOrNull { File(it, ".git").exists() && File(it, "android").isDirectory }
        ?: error("Cannot locate the repository")
    private val fixtures = root.resolve("spec/fixtures/lines")

    private fun replay(input: JsonObject): JsonElement {
        val output = NativeBridge.projectViewedImages(input.toString())
        assertTrue("JNI returned no viewed-image line", output.isNotEmpty())
        return Json.parseToJsonElement(output)
    }

    private fun assertLineEqual(label: String, expected: JsonElement, actual: JsonElement) {
        assertEquals(label, expected, actual)
    }

    @Test fun replayToolImagesWithTheCurrentAppleOverlay() {
        val case = fixture("tool_images_belong_to_call")
        val input = case.getValue("input").jsonObject
        val actual = replay(input)
        val overlay = Json.parseToJsonElement(fixtures.resolve("ios/differences.json").readText()).jsonObject
        val entry = overlay.getValue("cases").jsonArray.map { it.jsonObject }.single {
            it.getValue("name").jsonPrimitive.content == "tool_images_belong_to_call"
        }
        val expected = entry.getValue("replace").jsonObject.getValue("lines").jsonArray.map { it.jsonObject }.single {
            it.getValue("type").jsonPrimitive.content == "viewed"
        }
        assertLineEqual("tool_images_belong_to_call/current iOS viewed", expected, actual)
        assertTrue("the corpus still needs its Apple overlay", overlay.getValue("turn_wake_state").jsonObject.containsKey("drop"))
        val withoutPatch = case.getValue("expected").jsonObject.getValue("lines").jsonArray.map { it.jsonObject }.single {
            it.getValue("type").jsonPrimitive.content == "viewed"
        }
        assertNotEquals("the overlay must change the outer line id", withoutPatch, actual)
    }

    @Test fun rejectsMalformedInputWrongExpectationAndNonViewableCalls() {
        assertEquals("", NativeBridge.projectViewedImages("{\"invalid\":true}"))
        assertEquals("", NativeBridge.projectViewedImages("not JSON"))
        val case = fixture("tool_images_belong_to_call")
        val actual = replay(case.getValue("input").jsonObject)
        val wrong = buildJsonObject {
            put("id", "vi-2#0")
            put("image_ids", JsonArray(emptyList()))
            put("is_input", false)
            put("type", "viewed")
        }
        assertNotEquals("the negative viewed line differs", wrong, actual)
        val failure = assertThrows("the complete viewed-line comparison accepted a wrong expectation", AssertionError::class.java) {
            assertLineEqual("tool_images_belong_to_call/current iOS viewed", wrong, actual)
        }
        assertTrue(failure.message.orEmpty().contains("viewed"))
        for ((label, update) in mapOf<String, (MutableList<JsonObject>) -> Unit>(
            "running Read without images" to { items ->
                edit(items, "3#0") { use ->
                    use["tool_name"] = kotlinx.serialization.json.JsonPrimitive("Read")
                    use["tool_input"] = buildJsonObject { put("file_path", "picture.png") }
                }
                items.removeAll { it.getValue("kind").jsonPrimitive.content == "tool_result" }
                items.removeAll { it.getValue("kind").jsonPrimitive.content == "image" }
            },
            "failed Read without images" to { items ->
                edit(items, "3#0") { use ->
                    use["tool_name"] = kotlinx.serialization.json.JsonPrimitive("Read")
                    use["tool_input"] = buildJsonObject { put("file_path", "picture.png") }
                }
                items.removeAll { it.getValue("kind").jsonPrimitive.content == "image" }
                edit(items, "4#0") { result -> result["is_error"] = kotlinx.serialization.json.JsonPrimitive(true) }
            },
            "unsupported Read path" to { items ->
                edit(items, "3#0") { use ->
                    use["tool_name"] = kotlinx.serialization.json.JsonPrimitive("Read")
                    use["tool_input"] = buildJsonObject { put("file_path", "document.txt") }
                }
                items.removeAll { it.getValue("kind").jsonPrimitive.content == "image" }
            },
        )) {
            val items = case.getValue("input").jsonObject.getValue("items").jsonArray.map { it.jsonObject }.toMutableList()
            update(items)
            assertEquals("$label has no viewed line", "",
                NativeBridge.projectViewedImages(buildJsonObject { put("items", JsonArray(items)) }.toString()))
        }
        val duplicates = case.getValue("input").jsonObject.getValue("items").jsonArray.toMutableList()
        duplicates.add(duplicates[4])
        val deduplicated = replay(buildJsonObject { put("items", JsonArray(duplicates)) })
        assertEquals("one image result appears once", listOf("d:iVBORw0KGgo=12"),
            deduplicated.jsonObject.getValue("image_ids").jsonArray.map { it.jsonPrimitive.content })
        val erroredItems = case.getValue("input").jsonObject.getValue("items").jsonArray.map { it.jsonObject }.toMutableList()
        edit(erroredItems, "4#0") { result -> result["is_error"] = kotlinx.serialization.json.JsonPrimitive(true) }
        assertEquals("an actual image result remains visible when the tool failed", listOf("d:iVBORw0KGgo=12"),
            replay(buildJsonObject { put("items", JsonArray(erroredItems)) }).jsonObject
                .getValue("image_ids").jsonArray.map { it.jsonPrimitive.content })
    }

    @Test fun identityAndNoLineBoundariesMatchApple() {
        val case = fixture("tool_images_belong_to_call")
        val sourceImage = case.getValue("input").jsonObject.getValue("items").jsonArray
            .map { it.jsonObject }.single { it.getValue("kind").jsonPrimitive.content == "image" && it["tool_use_id"]?.jsonPrimitive?.contentOrNull == "call_a" }
        fun withImage(image: JsonObject): JsonElement {
            val items = case.getValue("input").jsonObject.getValue("items").jsonArray.map { it.jsonObject }.toMutableList()
            val index = items.indexOfFirst { it.getValue("kind").jsonPrimitive.content == "image" && it["tool_use_id"]?.jsonPrimitive?.contentOrNull == "call_a" }
            items[index] = image
            return replay(buildJsonObject { put("items", JsonArray(items)) })
        }
        val ref = "m1/s1/" + "x".repeat(60)
        val refImage = sourceImage.toMutableMap()
        refImage["image"] = buildJsonObject {
            put("mime", "image/png")
            put("data", "")
            put("ref", ref)
        }
        assertEquals("a lazy ref is never truncated or suffixed", listOf("d:$ref"),
            withImage(JsonObject(refImage)).jsonObject.getValue("image_ids").jsonArray.map { it.jsonPrimitive.content })
        val metadataImage = sourceImage.toMutableMap()
        metadataImage["image"] = buildJsonObject {
            put("mime", "image/png")
            put("data", "ignored")
            put("size", 900)
            put("width", 16)
            put("height", 10)
        }
        assertEquals("size metadata wins over inline identity", listOf("s:image/png:900:16x10"),
            withImage(JsonObject(metadataImage)).jsonObject.getValue("image_ids").jsonArray.map { it.jsonPrimitive.content })
        val unicodeImage = sourceImage.toMutableMap()
        unicodeImage["image"] = buildJsonObject {
            put("mime", "image/png")
            put("data", "é".repeat(64))
        }
        assertEquals("inline suffix counts Swift characters, not UTF-8 bytes", listOf("d:${"é".repeat(64)}64"),
            withImage(JsonObject(unicodeImage)).jsonObject.getValue("image_ids").jsonArray.map { it.jsonPrimitive.content })
        assertEquals("tool-call-less input has no viewed line", "", NativeBridge.projectViewedImages(buildJsonObject {
            put("items", JsonArray(emptyList()))
        }.toString()))
    }

    private fun edit(items: MutableList<JsonObject>, id: String, update: (MutableMap<String, JsonElement>) -> Unit) {
        val index = items.indexOfFirst {
            it.getValue("id").jsonPrimitive.content == id &&
                it.getValue("kind").jsonPrimitive.content in setOf("tool_use", "tool_result")
        }
        assertTrue("mutation target $id is a tool call or result", index >= 0)
        val target = items[index].toMutableMap()
        update(target)
        items[index] = JsonObject(target)
    }

    private fun fixture(name: String): JsonObject = requireNotNull(fixtures.listFiles { file -> file.isFile && file.extension == "json" })
        .flatMap { Json.parseToJsonElement(it.readText()).jsonObject.getValue("cases").jsonArray }
        .map { it.jsonObject }.first { it.getValue("name").jsonPrimitive.content == name }
}
