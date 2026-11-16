package persona.core.contracts.api

import persona.core.contracts.StandingAuthorizationList

/**
 * The current assistant's standing authorizations (GET and DELETE
 * `/api/v1/assistant/standing-authorizations`). A device without the full-access role is answered 403.
 */
interface StandingAuthorizationsSource {
    suspend fun list(): StandingAuthorizationList
    suspend fun revoke(id: String)
}
