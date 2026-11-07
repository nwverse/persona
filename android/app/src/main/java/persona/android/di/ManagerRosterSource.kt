package persona.android.di

import android.app.Application
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.merge
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import persona.android.PersonaApp
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubManagers
import persona.core.contracts.api.ManagerRoster
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The selected hub's roster of managers for the whole app (docs/assistant/managers.md step 4): read when the hub comes online
 * and on its `managers` pushes, given to the pages as `LocalManagerRoster`. It also keeps the assistant page's
 * [AssistantManagerChoice] on this hub: each hub restores its own choice across launches, and a manager that went away
 * falls back to the default after a successful roster read.
 */
@Singleton
class ManagerRosterSource @Inject constructor(application: Application, private val managers: HubManagers, stacks: GroupStacks) {
    private val app = application as PersonaApp
    private val selection = AssistantSelectionPreferences(
        application.getSharedPreferences(AssistantSelectionPreferences.FILE, Application.MODE_PRIVATE),
    )
    private val mutable = MutableStateFlow<ManagerRoster?>(null)
    val roster: StateFlow<ManagerRoster?> = mutable.asStateFlow()
    /** The group whose hub the roster is (null: the primary): its computers are the ones Move to… may give. */
    val group: StateFlow<String?> = stacks.selected.map { it?.namespace }
        .stateIn(app.scope, kotlinx.coroutines.flow.SharingStarted.Eagerly, stacks.selected.value?.namespace)

    private val started = java.util.concurrent.atomic.AtomicBoolean()

    /** Once per process (the first activity starts it). */
    fun start() {
        if (!started.compareAndSet(false, true)) return
        app.scope.launch {
            AssistantManagerChoice.current.collect { selection.remember(it) }
        }
        app.scope.launch {
            var lastHub: String? = null
            app.hub.collectLatest { hub ->
                mutable.value = null
                val hubId = hub?.connection?.http?.hubId
                // Under this hub before anything lists: what its lists note before the roster lands is found by its requests.
                selection.atHub(hubId)
                // What another hub's lists showed is not this one's (the first one's are kept: they may be read already).
                if (hubId != lastHub) { if (lastHub != null) AssistantManagerChoice.forgetItems(); lastHub = hubId }
                if (hub == null) return@collectLatest
                // Online again, or the hub said the roster changed: read it.
                merge(hub.connection.state.filter { it == ConnectionState.Online }.map { }, managers.changes).collect {
                    val read = try { managers.roster() } catch (cancelled: CancellationException) { throw cancelled } catch (_: Exception) { null }
                    if (read != null) {
                        mutable.value = read
                        keepChoice(hubId, read)
                    }
                }
            }
        }
    }

    private fun keepChoice(hubId: String?, roster: ManagerRoster) {
        selection.reconcile(hubId, roster)
    }

}
