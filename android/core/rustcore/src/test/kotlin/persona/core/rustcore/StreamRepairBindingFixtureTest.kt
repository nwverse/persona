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

/** Real Kotlin/JNI replay for fence and final-marker StreamRepair, with independently stated Apple expectations. */
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

    @Test fun holdsOnlyTheUnfinishedBlockMarkerLineBack() {
        // StreamRepairTests.holdsALastLineOfOnlyMarkersBack: exact Apple inputs and expected strings.
        val cases = listOf(
            "- 第一项\n-" to "- 第一项\n",
            "- 第一项\n- " to "- 第一项\n",
            "1. 第一步\n2." to "1. 第一步\n",
            "1. 第一步\n12" to "1. 第一步\n",
            "正文\n##" to "正文\n",
            "正文\n>" to "正文\n",
            "标题\n---" to "标题\n",
            "- [ ]" to "",
            "**" to "",
            "- 第" to "- 第",
            "2026年" to "2026年",
            "1.5 倍" to "1.5 倍",
            "-\n" to "-\n",
            "正文\n---\n" to "正文\n---\n",
        )
        for ((input, repaired) in cases) {
            assertEquals("StreamRepair markersOnly case: ${input.replace("\n", "\\n")}", repaired,
                replay(input).getValue("repaired").jsonPrimitive.content)
        }
        val wrong = expected("", "text")
        val actual = replay("正文\n##")
        assertNotEquals("a marker-only expected from another input must differ", wrong, actual)
        assertThrows("wrong complete marker expectation is rejected", AssertionError::class.java) {
            assertEquals("StreamRepair markersOnly negative", wrong, actual)
        }
        assertEquals("a digit beyond Apple's nine-digit ordered-item boundary is text", "1234567890",
            replay("1234567890").getValue("repaired").jsonPrimitive.content)
        assertEquals("digits after a marker are not an ordered-item opener", "- 12",
            replay("- 12").getValue("repaired").jsonPrimitive.content)
        assertEquals("a non-ASCII digit is real text", "１２", replay("１２").getValue("repaired").jsonPrimitive.content)
        println("JNI StreamRepair markersOnly replay: ${cases.size} repaired expectations plus boundaries")
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
