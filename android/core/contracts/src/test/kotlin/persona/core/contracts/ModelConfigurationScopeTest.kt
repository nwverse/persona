package persona.core.contracts

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.*

class ModelConfigurationScopeTest {
    @Test fun capturedOwnersSurviveSelectionChangesAndLateReplies() = runBlocking {
        val entered = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val calls = mutableListOf<Pair<String, String?>>()
        val item = ModelPurpose("agent.new")
        val source = object : ModelConfiguration {
            private suspend fun record(operation: String) {
                calls += operation to currentCoroutineContext()[RequestManager]?.id
            }
            override suspend fun catalog(): ModelCatalog {
                record("catalog")
                if (currentCoroutineContext()[RequestManager]?.id == "assistant") {
                    entered.complete(Unit); release.await()
                }
                return ModelCatalog(listOf(item))
            }
            override suspend fun purpose(id: String): ModelPurposeDetail {
                record("purpose"); return ModelPurposeDetail(item)
            }
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave {
                record("save"); return ModelPurposeSave.Saved(ModelPurposeDetail(item))
            }
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? {
                record("provider"); return null
            }
        }
        val first = source.forManager("assistant")
        val second = first.forManager("work")
        val pending = async { first.catalog() }
        entered.await()
        second.catalog(); second.purpose("agent.new")
        second.save(item, ModelConfigurationDraft()); second.providerModels("demo", false, null)
        release.complete(Unit); pending.await()
        first.save(item, ModelConfigurationDraft())
        assertEquals(listOf("catalog" to "assistant", "catalog" to "work", "purpose" to "work",
            "save" to "work", "provider" to "work", "save" to "assistant"), calls)
        assertNull(currentCoroutineContext()[RequestManager])
        assertTrue(runCatching { source.forManager("") }.exceptionOrNull() is IllegalArgumentException)
    }
}
