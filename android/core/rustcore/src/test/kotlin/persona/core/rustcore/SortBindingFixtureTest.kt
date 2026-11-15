package persona.core.rustcore

import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Drives the actual Rust JNI implementation through Kotlin, not merely its `external fun` declaration.
 * The complete expected projection is compared for every agents_list fixture. No Android device is involved.
 */
class SortBindingFixtureTest {
    private val fixtures: File = generateSequence(File(".").canonicalFile) { it.parentFile }
        .firstOrNull { File(it, ".git").exists() && File(it, "android").isDirectory }
        ?.resolve("spec/fixtures/sort") ?: error("Cannot locate spec/fixtures/sort")

    @Test fun replayEveryProjectionThroughJni() {
        val files = requireNotNull(fixtures.listFiles { file -> file.isFile && file.extension == "json" })
            .sortedBy { it.name }
        assertEquals(
            "sort fixture file set",
            listOf("filters.json", "folding.json", "grouping.json", "hub_state.json", "ordering.json", "session_state.json"),
            files.map { it.name },
        )
        val replayed = mutableSetOf<String>()
        for (file in files) {
            val document = Json.parseToJsonElement(file.readText()).jsonObject
            if (document.getValue("operation").jsonPrimitive.content != "agents_list") continue
            for (case in document.getValue("cases").jsonArray) {
                val value = case.jsonObject
                val name = value.getValue("name").jsonPrimitive.content
                val actual = NativeBridge.projectSort(value.getValue("input").jsonObject.toString())
                assertTrue("$file/$name: JNI returned no projection", actual.isNotEmpty())
                assertEquals("$file/$name", value.getValue("expected"), Json.parseToJsonElement(actual))
                assertTrue("duplicate projection case $name", replayed.add(name))
            }
        }
        assertEquals("JNI must replay every agents_list case", 30, replayed.size)
        println("JNI sort host replay: ${replayed.size} cases, full expected equality")
    }

    @Test fun malformedInputIsRejected() {
        assertEquals("", NativeBridge.projectSort("{\"invalid\":true}"))
        assertEquals("", NativeBridge.projectSort("not JSON"))
    }
}
