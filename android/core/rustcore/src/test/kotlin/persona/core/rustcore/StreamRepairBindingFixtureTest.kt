package persona.core.rustcore

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/** Real Kotlin/JNI replay for the fence-only StreamRepair slice, with independently stated Apple-test expectations. */
class StreamRepairBindingFixtureTest {
    private fun replay(text: String): JsonObject {
        val input = buildJsonObject { put("text", text) }.toString()
        val output = NativeBridge.projectStreamRepair(input)
        assertTrue("JNI returned no fence-repair projection", output.isNotEmpty())
        return Json.parseToJsonElement(output) as JsonObject
    }

    private fun expected(repaired: String, end: String): JsonObject = buildJsonObject {
        put("repaired", repaired)
        put("end", end)
    }

    @Test fun fenceRepairAndEndMatchTheAppleSourceCases() {
        // StreamRepairTests.closesAnOpenCodeFenceWithItsOwnMarker / holdsAFencesOpeningLineBack... / tellsWhereTheTextEnds.
        val repairCases = listOf(
            "text\n```ts\nconst a" to "text\n```ts\nconst a\n```",
            "~~~~\nx\n" to "~~~~\nx\n~~~~",
            "```\na\n```\ndone" to "```\na\n```\ndone",
            "看这段：\n```swi" to "看这段：\n",
            "```mermaid" to "",
            "看这段：\n```swift\n" to "看这段：\n```swift\n```",
            "```\nlet a = 1\n``" to "```\nlet a = 1\n```",
            "````\nx\n```" to "````\nx\n````",
            "````\nx\n```\n" to "````\nx\n```\n````",
            "prefix\n````\nx\n```\n" to "prefix\n````\nx\n```\n````",
            "```\n**x [a](b" to "```\n**x [a](b\n```",
            "```\n-" to "```\n-\n```",
            "1. 安装依赖：\n\n   ```bash\n   npm install" to "1. 安装依赖：\n\n   ```bash\n   npm install\n   ```",
            "1. 安装依赖：\n   ```bash\n   npm install\n" to "1. 安装依赖：\n   ```bash\n   npm install\n   ```",
            "- 运行：\n  ~~~\n  make" to "- 运行：\n  ~~~\n  make\n  ~~~",
            "- 运行：\n  ```\n  make\n  ``" to "- 运行：\n  ```\n  make\n  ```",
            "```\na\n  " to "```\na\n```",
            "这是 **重要" to "这是 **重要",
            "```swift\nlet a\n```" to "```swift\nlet a\n```",
            "```swift\nlet a\n```\n" to "```swift\nlet a\n```\n",
            "" to "",
        )
        for ((input, repaired) in repairCases) {
            assertEquals("StreamRepair fence case: ${input.replace("\n", "\\n")}", repaired, replay(input).getValue("repaired").jsonPrimitive.content)
        }
        val endCases = listOf(
            "" to "text",
            "这是 **重要" to "text",
            "看这段：\n```swi" to "code",
            "```swift\nlet a" to "code",
            "```swift\nlet a\n```" to "code",
            "```swift\nlet a\n```\n" to "text",
        )
        for ((input, end) in endCases) {
            assertEquals("StreamRepair end case: ${input.replace("\n", "\\n")}", end, replay(input).getValue("end").jsonPrimitive.content)
        }
        println("JNI StreamRepair fence replay: ${repairCases.size} repaired + ${endCases.size} end expectations")
    }

    @Test fun rejectsMalformedInputAndWrongExpectedObjects() {
        assertEquals("", NativeBridge.projectStreamRepair("{\"invalid\":true}"))
        assertEquals("", NativeBridge.projectStreamRepair("not JSON"))
        val input = "text\n```ts\nconst a"
        val actual = replay(input)
        val wrong = expected("text\n```ts\nconst a\n~~~", "text")
        assertNotEquals("wrong fence marker and end must differ", wrong, actual)
        val failure = assertThrows("a wrong complete expected object is rejected", AssertionError::class.java) {
            assertEquals("StreamRepair fence negative", wrong, actual)
        }
        assertTrue(failure.message.orEmpty().contains("StreamRepair fence negative"))
        assertEquals("an unfinished non-fence line is not a Markdown repair slice", expected("unfinished **", "text"), replay("unfinished **"))
    }
}
