package persona.core.data

import java.io.File
import java.time.Instant
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Machine
import persona.core.domain.sort.AgentGrouping
import persona.core.domain.sort.AgentListSettings
import persona.core.domain.sort.AgentPreferences
import persona.core.domain.sort.AgentQuickFilter
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.AgentSort
import persona.core.rustcore.RustCoreSort

/** Runs production Kotlin and the real host JNI projection on the same converted fixture input. */
class AgentListAdapterFixtureTest {
    private val fixtures = generateSequence(File(".").canonicalFile) { it.parentFile }
        .firstOrNull { File(it, ".git").exists() && File(it, "android").isDirectory }
        ?.resolve("spec/fixtures/sort") ?: error("Cannot locate spec/fixtures/sort")

    @Test fun kotlinAndRustReplayTheSameInputs() {
        val reports = mutableListOf<AgentListMismatch>()
        val shadow = ShadowAgentListAdapter(projectRust = RustCoreSort::project, sink = reports::add)
        val names = mutableSetOf<String>()
        for (file in requireNotNull(fixtures.listFiles()).sortedBy { it.name }) {
            if (file.extension != "json") continue
            val document = Json.parseToJsonElement(file.readText()).jsonObject
            if (document.getValue("operation").jsonPrimitive.content != "agents_list") continue
            for (case in document.getValue("cases").jsonArray) {
                val value = case.jsonObject
                val name = value.getValue("name").jsonPrimitive.content
                assertTrue("duplicate projection case $name", names.add(name))
                shadow.project(value.getValue("input").jsonObject.adapterInput())
                ShadowAgentListDiagnostics.awaitIdle()
            }
        }
        assertEquals(30, names.size)
        assertEquals("production adapter parity: $reports", 0, shadow.mismatchCount)
    }
}

internal fun JsonObject.adapterInput(): AgentListAdapter.Input {
    fun strings(value: JsonObject, key: String) = value[key]?.jsonArray.orEmpty().map { it.jsonPrimitive.content }
    val preferences = getValue("pins").jsonObject.let { AgentPreferences(strings(it, "spaces"), strings(it, "sessions")) }
    val mutes = getValue("mutes").jsonObject.let {
        AgentPreferences(strings(it, "spaces"), strings(it, "sessions"), strings(it, "allowed").toSet())
    }
    val settings = getValue("settings").jsonObject
    fun strings(key: String) = settings[key]?.jsonArray.orEmpty().map { it.jsonPrimitive.content }.toSet()
    return AgentListAdapter.Input(
        sessions = getValue("sessions").jsonArray.map { AgentSession.fromJson(it.jsonObject) },
        machines = getValue("machines").jsonArray.map { Machine(it.jsonObject.getValue("id").jsonPrimitive.content, it.jsonObject.getValue("name").jsonPrimitive.content) },
        pins = preferences,
        mutes = mutes,
        settings = AgentListSettings(
            grouping = when (settings.getValue("grouping").jsonPrimitive.content) {
                "state" -> AgentGrouping.State
                "space" -> AgentGrouping.Space
                "machine" -> AgentGrouping.Machine
                else -> error("Unsupported fixture grouping")
            },
            quick = when (settings.getValue("quick").jsonPrimitive.content) {
                "all" -> AgentQuickFilter.All
                "needsYou" -> AgentQuickFilter.NeedsYou
                "unread" -> AgentQuickFilter.Unread
                "working" -> AgentQuickFilter.Working
                "backgroundWaiting" -> AgentQuickFilter.BackgroundWaiting
                else -> error("Unsupported fixture quick filter")
            },
            query = settings["query"]?.jsonPrimitive?.content.orEmpty(),
            machineFilter = settings["machine_filter"]?.jsonPrimitive?.content.orEmpty(),
            kept = strings("kept"),
            folded = strings("folded"),
            expandedIdle = strings("expanded_idle"),
            expandedQuiet = strings("expanded_quiet"),
            sort = if (settings["sort"]?.jsonPrimitive?.content == "updated") AgentSort.Updated else AgentSort.State,
        ),
        held = settings["held"]?.jsonPrimitive?.contentOrNull,
        now = settings["now"]?.jsonPrimitive?.contentOrNull?.let(Instant::parse) ?: Instant.EPOCH,
    )
}
