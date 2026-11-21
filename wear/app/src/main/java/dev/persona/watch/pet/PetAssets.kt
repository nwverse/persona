package dev.persona.watch.pet

internal data class PetAssetRead(val manifest: PetManifest?, val failure: Throwable? = null)

/** In-memory metadata cannot prove the cache's files survived eviction or a failed refresh. */
internal fun readPetAssets(force: Boolean, cached: () -> PetManifest?, fetch: () -> PetManifest): PetAssetRead {
    val usable = runCatching(cached).getOrNull()
    if (!force && usable != null) return PetAssetRead(usable)
    val result = runCatching(fetch)
    return PetAssetRead(result.getOrNull() ?: runCatching(cached).getOrNull(), result.exceptionOrNull())
}
