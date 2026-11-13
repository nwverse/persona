package persona.feature.agents

import persona.core.contracts.JsonCodec
import persona.core.contracts.Machine
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** New Agent's recent setups by the rules every client shares: each case of spec/fixtures/recents/recent_setups.json. */
class RecentSetupsFixtureTest {
    private val fixture: JsonObject by lazy {
        val root = System.getProperty("persona.repo.root") ?: error("persona.repo.root is required")
        JsonCodec.parseToJsonElement(File(root, "spec/fixtures/recents/recent_setups.json").readText()).jsonObject
    }

    private fun JsonObject.setups(key: String) = getValue(key).jsonArray.map { requireNotNull(RecentSetups.fromJson(it.jsonObject)) }
    private fun JsonObject.text(key: String) = getValue(key).jsonPrimitive.content

    @Test fun limitsMatchTheFixtures() {
        assertEquals(1, fixture.getValue("schema_version").jsonPrimitive.int)
        assertEquals(fixture.getValue("max_entries").jsonPrimitive.int, RecentSetups.MAX_ENTRIES)
        assertEquals(fixture.getValue("max_cards").jsonPrimitive.int, RecentSetups.MAX_CARDS)
    }

    @Test fun everyCase() {
        val cases = fixture.getValue("cases").jsonArray.map { it.jsonObject }
        val operations = cases.map { it.text("operation") }.toSet()
        assertEquals(setOf("record", "cards", "model"), operations)
        cases.forEach { case ->
            val name = case.text("name")
            val input = case.getValue("input").jsonObject
            val expected = case.getValue("expected").jsonObject
            when (case.text("operation")) {
                "record" -> {
                    val setup = requireNotNull(RecentSetups.fromJson(input.getValue("setup").jsonObject))
                    val after = RecentSetups.record(input.setups("before"), setup)
                    assertEquals(name, expected.setups("after"), after)
                    // What the store writes reads back the same.
                    assertEquals(name, after, RecentSetups.decode(RecentSetups.encode(after)))
                }
                "cards" -> {
                    val machines = input.getValue("machines").jsonArray.map {
                        Machine(it.jsonObject.text("id"), online = it.jsonObject.getValue("online").jsonPrimitive.boolean)
                    }
                    val entries = input.setups("entries")
                    val cards = RecentSetups.cards(entries, machines)
                    val want = expected.getValue("cards").jsonArray.map { it.jsonObject }
                    assertEquals(name, want.map { it.getValue("index").jsonPrimitive.int to it.getValue("online").jsonPrimitive.boolean },
                        cards.map { it.index to it.online })
                    cards.forEach { assertEquals(name, entries[it.index], it.setup) }
                }
                "model" -> assertEquals(name, expected.text("model"),
                    RecentSetups.model(input.setups("entries"), input.text("machine"), input.text("launcher")))
                else -> error("$name: unknown operation")
            }
        }
    }

    @Test fun unreadableRecordsAreNoSetups() {
        assertTrue(RecentSetups.decode(null).isEmpty())
        assertTrue(RecentSetups.decode("not json").isEmpty())
        assertTrue(RecentSetups.decode("{\"machine\":\"mac-a\"}").isEmpty())
        assertEquals(listOf(RecentSetup("mac-a", "/code/persona")),
            RecentSetups.decode("[{\"machine\":\"mac-a\",\"cwd\":\"/code/persona\"},{\"machine\":\"mac-a\"},3]"))
    }

    @Test fun aCardsFolderIsItsLastComponentOrHome() {
        assertEquals("persona", recentFolder("/code/persona", "/home/me"))
        assertEquals("persona", recentFolder("/code/persona/", null))
        assertEquals("~", recentFolder("/home/me", "/home/me/"))
        assertEquals("~", recentFolder("~", null))
        assertEquals("/", recentFolder("/", null))
    }
}
