package persona.core.moduleapi

/**
 * The modules a build carries, in the order their entries show (the app's `Modules.kt` builds it). A module is active on
 * a hub when the hub offers its [PersonaModule.feature], or always when it needs none; everything else is absent.
 */
class ModuleRegistry(val modules: List<PersonaModule>) {
    init {
        modules.forEach { require(it.id.matches(ModuleId)) { "Module id '${it.id}' is not lower-case and hyphenated" } }
        val repeated = modules.groupBy { it.id }.filterValues { it.size > 1 }.keys
        require(repeated.isEmpty()) { "Modules listed twice: $repeated" }
    }

    /** The modules active on a hub whose hello offers [features]. */
    fun active(features: Set<String>): List<PersonaModule> = modules.filter { it.feature == null || it.feature in features }

    /** Each module's [PersonaModule.services] for one pairing, by id; modules that keep none are left out. */
    fun services(hub: ModuleHub): Map<String, ModuleServices> =
        modules.mapNotNull { module -> module.services(hub)?.let { module.id to it } }.toMap()

    /** The debug pages of every module ([PersonaModule.debugPages]). */
    fun debugPages() = modules.flatMap { it.debugPages().entries }.associate { it.key to it.value }

    companion object {
        val Empty = ModuleRegistry(emptyList())
        private val ModuleId = Regex("[a-z][a-z0-9]*(-[a-z0-9]+)*")
    }
}
