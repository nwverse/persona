package persona.android.di

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import persona.core.contracts.api.AssistantManagement
import persona.core.contracts.api.StandingAuthorizationsSource

/**
 * One hub's standing-authorizations client, still speaking that hub's plain machine ids.
 * [identity] is the group id the page keys on. [names] turns those ids into the merged lists' ids.
 */
internal class StandingHub(
    val identity: String,
    val names: GroupNames,
    val management: AssistantManagement,
)

/**
 * Settings › Standing Authorizations for the selected group. Reads go through [AssistantManagement.inGroup], the same
 * translation the assistant profile's count uses, so a computer's name is that hub's and not another hub's machine
 * that happens to share the raw id. [pinned] keeps revoke on the hub that listed the row.
 */
internal class SelectedGroupStandingAuthorizations(
    private val current: () -> StandingHub,
    override val hubIdentity: StateFlow<String>,
) : StandingAuthorizationsSource {
    private fun translated(hub: StandingHub): AssistantManagement = hub.management.inGroup(hub.names)

    override fun pinned(): StandingAuthorizationsSource {
        val hub = current()
        val api = translated(hub)
        return object : StandingAuthorizationsSource {
            override val hubIdentity: StateFlow<String> = MutableStateFlow(hub.identity)
            override fun pinned(): StandingAuthorizationsSource = this
            override suspend fun list() = api.standingAuthorizations()
            override suspend fun revoke(id: String) = api.revokeStandingAuthorization(id)
        }
    }

    override suspend fun list() = translated(current()).standingAuthorizations()
    override suspend fun revoke(id: String) = translated(current()).revokeStandingAuthorization(id)
}
