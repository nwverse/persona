package persona.core.contracts.api

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Settings › Managers (docs/assistant/managers.md, docs/protocol/protocol.md"Managers"): the selected hub's roster of managers, each a whole
 * assistant, and the one-time install codes that put a new one (or one moved elsewhere) on a computer. The hub answers
 * these itself (`/api/v1/managers`). Bound to the active pairing's selected hub.
 */
interface HubManagers {
    /** The hub's `managers` pushes (sent only to an app that asks for the `managers` feature): read the list again. */
    val changes: Flow<Unit> get() = emptyFlow()

    /** `GET /api/v1/managers`; a hub from before the roster is a [ManagersUnsupported]. */
    suspend fun roster(): ManagerRoster

    /** `POST /api/v1/managers {name}`: a new manager, still installing, and its install code. */
    suspend fun create(name: String): ManagerCode

    /** `POST /api/v1/managers/{id}/code`: a new install code (moving it elsewhere, or one whose code ran out). */
    suspend fun newCode(id: String): ManagerCode

    /** `PATCH /api/v1/managers/{id} {name}`. */
    suspend fun rename(id: String, name: String): HubManager

    /** `PATCH /api/v1/managers/{id} {default: true}`. */
    suspend fun makeDefault(id: String): HubManager

    /** `DELETE /api/v1/managers/{id}`: its token stops working at once (an installing one's code is void). */
    suspend fun delete(id: String)
}

/** The hub has no `/api/v1/managers` (404): it predates the roster. */
class ManagersUnsupported : Exception("This hub has no managers")

/** GET /api/v1/managers: the default first, the installing ones last. */
@Serializable
data class ManagerRoster(
    val managers: List<HubManager> = emptyList(),
    @SerialName("default") val defaultId: String = "",
) {
    /** The installed managers (an installing one is not there yet), the default first, then in the roster's order. */
    val installed: List<HubManager> get() = managers.filterNot { it.isInstalling }
        .sortedBy { if (it.id == defaultManagerId) 0 else 1 }

    /** More than one installed manager: the pages group by manager and the assistant page offers the switcher. */
    val isMulti: Boolean get() = installed.size > 1

    /** The default manager's id: the roster's `default`, else the one marked default, else the hub's own assistant. */
    val defaultManagerId: String get() = defaultId.ifBlank { managers.firstOrNull { it.isDefault }?.id ?: ASSISTANT_MANAGER }

    fun manager(id: String?): HubManager? = managers.firstOrNull { it.id == id }

    /** Which manager an item stamped with [manager] belongs to: no stamp is the default manager's. */
    fun ownerOf(manager: String?): String = manager?.takeIf { it.isNotBlank() } ?: defaultManagerId

    /**
     * [items] by the manager they belong to ([managerOf]; no stamp is the default's): the installed managers in
     * [installed]'s order, empty groups left out; items of a manager the roster does not list (deleted, still
     * installing) come last, by their id. Null with one manager: nothing groups then.
     */
    fun <T> groups(items: List<T>, managerOf: (T) -> String?): List<ManagerGroup<T>>? {
        if (!isMulti) return null
        val byOwner = items.groupBy { ownerOf(managerOf(it)) }
        val known = installed.mapNotNull { m -> byOwner[m.id]?.let { ManagerGroup(m.id, m, it) } }
        val ids = installed.map { it.id }.toSet()
        val others = byOwner.keys.filter { it !in ids }.sorted().map { id -> ManagerGroup(id, manager(id), byOwner.getValue(id)) }
        return known + others
    }

    companion object {
        /** The hub's own assistant's id: what an unstamped live update comes from. */
        const val ASSISTANT_MANAGER = "assistant"
    }
}

/** One manager's share of a list; [manager] is null for an id the roster does not list. */
data class ManagerGroup<T>(val id: String, val manager: HubManager?, val items: List<T>)

/**
 * Which manager the assistant page shows (docs/assistant/managers.md step 4): null is the default manager, and requests go as
 * before; an id puts `X-Persona-Manager` on the assistant's requests (`/api/v1/assistant…`) to [hubId]'s hub, and the
 * page lists that manager's conversations. Live updates that name a conversation (feed items, cards, plans) go by it,
 * whichever manager's it is, so another manager's conversation open elsewhere stays live. Process-wide like
 * [persona.core.contracts.ClientLanguage]: the assistant page sets it, the hub client and the assistant's repositories
 * read it.
 */
object AssistantManagerChoice {
    /** [manager] null: the default; [defaultId] is the roster's default (whose conversations the page lists then). */
    data class Choice(val hubId: String? = null, val manager: String? = null, val defaultId: String = ManagerRoster.ASSISTANT_MANAGER) {
        /** The manager the page shows. */
        val shown: String get() = manager ?: defaultId.ifBlank { ManagerRoster.ASSISTANT_MANAGER }

        /** Whether an item stamped [stamp] (absent: the default manager's) is the shown manager's. */
        fun owns(stamp: String?): Boolean = (stamp?.takeIf { it.isNotBlank() } ?: defaultId.ifBlank { ManagerRoster.ASSISTANT_MANAGER }) == shown
    }

    private val state = MutableStateFlow(Choice())
    val current: StateFlow<Choice> = state.asStateFlow()

    /** Shows [manager] of [hubId]'s hub (null or the default: the default, with no header). */
    fun choose(hubId: String?, manager: String?, defaultId: String) {
        val id = manager?.takeIf { it.isNotBlank() && it != defaultId }
        state.value = Choice(hubId, id, defaultId.ifBlank { ManagerRoster.ASSISTANT_MANAGER })
    }

    /** The assistant page picked [manager] (null or the default: the default) of the hub it is on now. */
    fun pick(manager: String?) = state.value.let { choose(it.hubId, manager, it.defaultId) }

    /** Back to the default (another hub, unpaired, a test's end). */
    fun reset() { state.value = Choice() }

    /**
     * Now on [hubId]'s hub, its roster not read yet: the default, under that hub, so what its lists note before the
     * roster lands is kept under it (and found when its requests ask).
     */
    fun atHub(hubId: String?) { state.value = Choice(hubId) }

    // The things a merged list showed with their manager (an inbox item) and the conversations each manager's page
    // listed, by hub and path under /api/v1 (`inbox/<id>`, `assistant/conversations/<id>`): requests for one name its
    // manager, as the hub asks ("When a list item carries manager, include it in requests for that item"). Every stamp
    // is kept; whether it needs a header is decided when asked, against the default then (the roster may move it). The
    // web keeps the same registry (manager-choice.ts).
    private val items = java.util.concurrent.ConcurrentHashMap<Pair<String?, List<String>>, String>()

    /** A merged list of [hubId]'s hub (the one shown now) showed the thing at [path] (segments under /api/v1) as [manager]'s. */
    fun noteItem(path: List<String>, manager: String?, hubId: String? = state.value.hubId) {
        if (path.isEmpty() || path.any { it.isEmpty() }) return
        val id = manager?.takeIf { it.isNotBlank() }
        if (id == null) items.remove(hubId to path) else items[hubId to path] = id
    }

    /**
     * The manager a request to [path] (segments under /api/v1) of [hubId]'s hub is for, from a list that showed it (or
     * the thing it is under); null: send none (none known, or the default). A page's own pins are [ManagerPins].
     */
    fun itemHeader(path: List<String>, hubId: String? = state.value.hubId): String? {
        val default = state.value.defaultId
        for (n in path.size downTo 1) {
            val owner = items[hubId to path.subList(0, n)] ?: continue
            return owner.takeIf { it != default }
        }
        return null
    }

    /** Another hub: what the last one's lists showed is not this one's. */
    fun forgetItems() = items.clear()

    /** The `X-Persona-Manager` value for an assistant request to the hub [hubId]; null: send none. */
    fun header(hubId: String?): String? = state.value.takeIf { it.hubId == hubId }?.manager
}

/** One manager as the roster has it; times are RFC 3339. */
@Serializable
data class HubManager(
    val id: String,
    val name: String = "",
    val online: Boolean = false,
    @SerialName("default") val isDefault: Boolean = false,
    /** The assistant the hub starts beside itself: it cannot be deleted or moved. */
    val builtin: Boolean = false,
    val host: String? = null,
    val os: String? = null,
    val arch: String? = null,
    /** How it runs: child (the hub's own), service / systemd / launchd, docker / container, external, or other words. */
    val deploy: String? = null,
    val version: String? = null,
    /** Connected since (online only). */
    val since: String? = null,
    /** Last connected (offline: Last synced at). */
    @SerialName("last_seen") val lastSeen: String? = null,
    /** Made in the app, its install code not traded yet: when that code runs out. */
    val installing: String? = null,
    /** Online on a build that reads no settings of its own (no `config.own`): it runs on built-in defaults until updated. */
    @SerialName("needs_update") val needsUpdate: Boolean = false,
) {
    val isInstalling: Boolean get() = !installing.isNullOrBlank()
}

/** A one-time install code (10 minutes, used once) and, from a hub that gives them, the commands that use it. */
@Serializable
data class ManagerCode(
    val code: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    /** POST /api/v1/managers also answers the new manager. */
    val manager: HubManager? = null,
    val commands: ManagerCommands? = null,
)

/** Ready-made install commands with the code in them; either may be missing. */
@Serializable
data class ManagerCommands(val service: String? = null, val docker: String? = null)

/**
 * The manager the hub requests made inside it are for (an inbox item's, docs/assistant/managers.md "Clients"): the connection
 * names it on each request (`?manager=`) where the request is made, so one still going out after the page moved on
 * keeps it, and two things of the same id under two managers each go to their own. [id] "" is the default's: no manager
 * named, and no stamp a list noted for the path either.
 */
class RequestManager(val id: String) : kotlin.coroutines.AbstractCoroutineContextElement(Key) {
    companion object Key : kotlin.coroutines.CoroutineContext.Key<RequestManager>
}

/**
 * A page's pins (an extension's page, an inbox item's chat, mail or thread): the requests made inside it to a path under
 * one of its prefixes (segments under /api/v1) of [hubId]'s hub name its manager, as [RequestManager] does; the longest
 * prefix wins, and a [RequestManager] wins over it. It goes only with the page's own requests (the APIs it hands its
 * children carry it), so another page, a background refresh or the page under it on the stack is not pinned.
 */
class ManagerPins private constructor(private val prefixes: Map<List<String>, String>, val hubId: String?) :
    kotlin.coroutines.AbstractCoroutineContextElement(Key) {

    /** The manager pinned for [path], the default's id included; null: not pinned. */
    private fun owner(path: List<String>): String? = (path.size downTo 1).firstNotNullOfOrNull { prefixes[path.subList(0, it)] }

    /** What a request to [path] of [hubId]'s hub names: "" for the default (none, and no stamp); null: not pinned. */
    fun manager(path: List<String>, hubId: String? = AssistantManagerChoice.current.value.hubId): String? {
        if (hubId != this.hubId) return null
        val owner = owner(path) ?: return null
        return owner.takeIf { it != AssistantManagerChoice.current.value.defaultId } ?: ""
    }

    override fun equals(other: Any?) = other is ManagerPins && other.prefixes == prefixes && other.hubId == hubId
    override fun hashCode() = prefixes.hashCode() * 31 + hubId.hashCode()

    companion object Key : kotlin.coroutines.CoroutineContext.Key<ManagerPins> {
        /** Requests under each of [prefixes] name [manager]; null (nothing pinned) for no manager. */
        fun of(prefixes: List<List<String>>, manager: String?, hubId: String? = AssistantManagerChoice.current.value.hubId): ManagerPins? {
            val id = manager?.takeIf { it.isNotBlank() } ?: return null
            return ManagerPins(prefixes.filter { it.isNotEmpty() }.associateWith { id }, hubId)
        }
    }
}
