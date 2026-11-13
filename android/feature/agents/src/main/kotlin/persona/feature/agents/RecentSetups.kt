package persona.feature.agents

import persona.core.contracts.JsonCodec
import persona.core.contracts.Machine
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.put

/**
 * One setup that started an Agent from New Agent (#2134): ids only, kept on this device for one hub, never a message.
 * [machine] is the machine id as the hub lists it here (a non-primary group's carries its suffix); [launcher] "" follows
 * the model configuration and [model] "" is the launcher's default; [mode] and [effort] are "" unless [agentType] is
 * claude. [at] is when it started (RFC 3339).
 */
internal data class RecentSetup(
    val machine: String,
    val cwd: String,
    val launcher: String = "",
    val model: String = "",
    val agentType: String = "claude",
    val mode: String = "",
    val effort: String = "",
    val at: String = "",
) {
    /** The same setup, whenever it started. */
    fun same(other: RecentSetup): Boolean = copy(at = "") == other.copy(at = "")
}

/** A "Recent" card: [setup] is the entry at [index]; only an [online] machine's card can be picked. */
internal data class RecentCard(val index: Int, val setup: RecentSetup, val online: Boolean)

/** New Agent's recent setups, by the rules every client follows (spec/fixtures/recents/README.md). */
internal object RecentSetups {
    const val MAX_ENTRIES = 20
    const val MAX_CARDS = 3

    /** A setup that started an Agent goes first; the same setup is not kept twice; the first [MAX_ENTRIES] stay. */
    fun record(entries: List<RecentSetup>, setup: RecentSetup): List<RecentSetup> =
        (listOf(setup) + entries.filterNot { it.same(setup) }).take(MAX_ENTRIES)

    /** "Remove from Recent". */
    fun remove(entries: List<RecentSetup>, setup: RecentSetup): List<RecentSetup> = entries.filterNot { it.same(setup) }

    /**
     * The cards: the entries whose machine the hub still lists, in order, at most [MAX_CARDS]. The others are skipped,
     * not deleted, and age out as new setups come in.
     */
    fun cards(entries: List<RecentSetup>, machines: List<Machine>): List<RecentCard> {
        val online = machines.associate { it.id to it.online }
        return entries.withIndex().mapNotNull { (index, setup) -> online[setup.machine]?.let { RecentCard(index, setup, it) } }
            .take(MAX_CARDS)
    }

    /** The model New Agent picks with [machine] and its [launcher]: that of the machine's latest setup with that launcher; "": the default. */
    fun model(entries: List<RecentSetup>, machine: String, launcher: String): String =
        entries.firstOrNull { it.machine == machine && it.launcher == launcher }?.model.orEmpty()

    fun encode(entries: List<RecentSetup>): String = JsonArray(entries.map { it.toJson() }).toString()

    /** What [encode] wrote; anything unreadable is no setups, an entry without a machine or folder is skipped. */
    fun decode(raw: String?): List<RecentSetup> {
        if (raw.isNullOrBlank()) return emptyList()
        val array = try { JsonCodec.parseToJsonElement(raw).jsonArray } catch (_: Exception) { return emptyList() }
        return array.mapNotNull { (it as? JsonObject)?.let(::fromJson) }
    }

    /** One entry in the fixtures' and the store's shape (`agent_type`); null without a machine or a folder. */
    fun fromJson(value: JsonObject): RecentSetup? {
        fun text(key: String) = (value[key] as? JsonPrimitive)?.contentOrNull.orEmpty()
        val setup = RecentSetup(text("machine"), text("cwd"), text("launcher"), text("model"), text("agent_type").ifEmpty { "claude" },
            text("mode"), text("effort"), text("at"))
        return setup.takeIf { it.machine.isNotEmpty() && it.cwd.isNotEmpty() }
    }

    fun RecentSetup.toJson(): JsonObject = buildJsonObject {
        put("machine", machine)
        put("cwd", cwd)
        put("launcher", launcher)
        put("model", model)
        put("agent_type", agentType)
        put("mode", mode)
        put("effort", effort)
        put("at", at)
    }
}
