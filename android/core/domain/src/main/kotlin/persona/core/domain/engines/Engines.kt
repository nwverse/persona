package persona.core.domain.engines

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.DetectedEngine
import persona.core.contracts.Machine
import persona.core.contracts.api.NO_TERMINAL_ENGINE

/**
 * One terminal engine per machine, or none (docs/integrations/engines.md), as on iOS `EngineSelectionModel`; a set of several that
 * no single engine stands for is shown as it is and edited on the web ([isSet]). The options are what
 * the hub reports in `engines`; [known] only names the engines this build knows: for hubs that report nothing, and to decide
 * what may be chosen without detection (an offline machine). [NONE] is the empty host set: it is listed, and read back from
 * `engine_hosts`, only for a hub that reports host sets.
 */
object Engines {
    const val DEFAULT = "herdr"

    /** No terminal engine: not a hub engine id, saved as the empty host set and not in [known]. */
    const val NONE = NO_TERMINAL_ENGINE

    /** An engine this build knows: its id, a fallback name, and where its sessions live. */
    data class Known(val id: String, val name: String, val detail: CoreText)

    val known = listOf(
        Known("herdr", "herdr", CoreText.of(Key.EngineHerdr)),
        Known("herdr-core", "Herdr App", CoreText.of(Key.EngineHerdrApp)),
        Known("orca", "Orca", CoreText.of(Key.EngineOrca)),
        Known("tern", "Tern", CoreText.of(Key.EngineTern)),
        Known("rex", "Rex", CoreText.of(Key.EngineRex)),
        Known("paseo", "Paseo", CoreText.of(Key.EnginePaseo)),
        Known("cindy", "Cindy", CoreText.of(Key.EngineCindy)),
        Known("t3code", "T3 Code", CoreText.of(Key.EngineT3code)),
    )

    fun detail(id: String): CoreText? =
        if (id == NONE) CoreText.of(Key.EngineNoneDetail) else known.firstOrNull { it.id == id }?.detail

    /** The hub reports host sets and the machine enables none: `engine` still says herdr for it, so it cannot be read from there. */
    fun isNone(machine: Machine): Boolean = machine.engineHosts?.isEmpty() == true

    /** An older hub reports no host sets and would refuse the empty one: no such option there. */
    fun offersNone(machine: Machine): Boolean = machine.engineHosts != null

    /**
     * The chosen engine: [NONE] for the empty set, the default of a [isSet] set, else the machine's engine; herdr when the hub
     * sent none.
     */
    fun current(machine: Machine): String = when {
        isNone(machine) -> NONE
        isSet(machine) -> setDefault(machine)
        else -> machine.engine.ifBlank { DEFAULT }
    }

    /** The hosts a single engine stands for, as the hub's `EngineHostsOf`: herdr and Herdr App for herdr-both, else itself. */
    fun hostsOf(engine: String): List<String> = if (engine == HERDR_BOTH) listOf("herdr", "herdr-core") else listOf(engine)

    /** The default host a single engine stands for: herdr for herdr-both, else itself. */
    private fun defaultOf(engine: String): String = if (engine == HERDR_BOTH) "herdr" else engine

    private const val HERDR_BOTH = "herdr-both"

    /**
     * The machine enables several engines that no single option stands for (herdr, Orca and Paseo checked on the web, #17052):
     * the picker can show only one of them, so it lists the set, points to the web to edit it, and a pick inside it only moves
     * the default ([saveOf]). herdr with Herdr App and herdr the default is the herdr-both option, not a set.
     */
    fun isSet(machine: Machine): Boolean {
        val hosts = machine.engineHosts ?: return false
        if (hosts.size < 2) return false
        val default = machine.engineDefault?.takeIf { it.isNotBlank() } ?: machine.engine
        return hosts.toSet() != hostsOf(machine.engine).toSet() || default != defaultOf(machine.engine)
    }

    /** A set's default engine, where new sessions from the phone start; the machine's engine from a hub that sent no default. */
    fun setDefault(machine: Machine): String =
        machine.engineDefault?.takeIf { it.isNotBlank() && machine.engineHosts?.contains(it) == true } ?: machine.engine.ifBlank { DEFAULT }

    /** A set's engines by name, in the hub's order: "herdr + Orca + Paseo". */
    fun setNames(machine: Machine): String = machine.engineHosts.orEmpty().joinToString(" + ") { name(it, machine) }

    /** What the machine page says it runs: the set's engines when it is a [isSet] set, else [currentLabel]. */
    fun summary(machine: Machine): CoreText = if (isSet(machine)) CoreText.plain(setNames(machine)) else currentLabel(machine)

    /** The note above the picker for a [isSet] set: which engines, the default, and that the web edits the set; null otherwise. */
    fun setNotice(machine: Machine): CoreText? =
        if (isSet(machine)) CoreText.of(Key.EngineSetNotice, setNames(machine), name(setDefault(machine), machine)) else null

    /** Whether [selected] stays inside the machine's [isSet] set (it only becomes the default there). */
    private fun keepsSet(selected: String, machine: Machine): Boolean {
        if (selected == NONE || !isSet(machine)) return false
        val hosts = machine.engineHosts.orEmpty()
        return hostsOf(selected).all { it in hosts }
    }

    /** What Save sends (#17052). */
    sealed interface Save {
        /** One engine, or [NONE]: the set it stands for replaces the machine's. */
        data class Engine(val id: String) : Save

        /** The machine's set as it is, with [default] its default. */
        data class Default(val hosts: List<String>, val default: String) : Save
    }

    /**
     * What Save sends for [selected]: inside a [isSet] set the same set with it as the default, so even a hub that reads one
     * engine as a set of one keeps the others; any other pick is that engine alone, as before.
     */
    fun saveOf(selected: String, machine: Machine): Save =
        if (keepsSet(selected, machine)) Save.Default(machine.engineHosts.orEmpty(), defaultOf(selected)) else Save.Engine(selected)

    /**
     * Whether Save may send [selected]: an engine that can be chosen, and outside pairing only a change, so opening the page and
     * tapping Save never rewrites what was set elsewhere. Pairing confirms the first choice even when it is the one shown.
     */
    fun canSave(selected: String, machine: Machine, pairing: Boolean): Boolean =
        canSelect(selected, machine) && (pairing || selected != current(machine))

    /**
     * The engine a screen judges the machine by (herdr's stopped notice, the health features): [NONE] for the empty set, else the
     * reported `engine` as is. Unlike [current] a blank one stays blank, so a hub that reports no engine is read as before.
     */
    fun judged(machine: Machine): String = if (isNone(machine)) NONE else machine.engine

    /** The chosen engine's name: the hub's name for it, else the known one, else its id. */
    fun currentName(machine: Machine): String = name(current(machine), machine)

    /** [currentName] as words: [NONE] has no name the hub or this build could spell out, it is a localized one. */
    fun currentLabel(machine: Machine): CoreText = label(current(machine), machine)

    fun label(id: String, machine: Machine): CoreText = if (id == NONE) CoreText.of(Key.EngineNone) else CoreText.plain(name(id, machine))

    fun name(id: String, machine: Machine): String = machine.engines.firstOrNull { it.id == id }?.name?.takeIf { it.isNotBlank() }
        ?: if (id == NONE) Key.EngineNone.english else known.firstOrNull { it.id == id }?.name ?: id

    /** The machine pairing asks about: the hub's own (kind local), else the first one. */
    fun pairingMachine(machines: List<Machine>): Machine? = machines.firstOrNull { it.kind == "local" } ?: machines.firstOrNull()

    /** Pairing asks only when the hub reports engines: an older hub cannot save a choice and the step must not trap. */
    fun pairingStep(machines: List<Machine>): Machine? = pairingMachine(machines)?.takeIf { it.engines.isNotEmpty() }

    /**
     * What the picker starts on: the machine's current engine, herdr when never chosen. Never guessed from what happens
     * to be detected (2026-11-01, the user: an engine must be picked, herdr by default).
     */
    fun initialChoice(machine: Machine): String = current(machine)

    /**
     * The rows to list: the hub's `engines` as given; with none reported, the known engines (not detected). No terminal engine
     * comes last, and only from a hub that reports host sets.
     */
    fun options(machine: Machine): List<DetectedEngine> {
        val detected = machine.engines.ifEmpty { known.map { DetectedEngine(it.id, it.name, available = false) } }
        return if (offersNone(machine)) detected + DetectedEngine(NONE, Key.EngineNone.english, available = true) else detected
    }

    /**
     * Known engines can be set without detection (offline: applied on connect); an unknown one only when found there. No
     * terminal engine needs the hub's host sets, and nothing to detect.
     */
    fun canSelect(id: String, machine: Machine): Boolean =
        if (id == NONE) offersNone(machine) else known.any { it.id == id } || machine.engines.any { it.id == id && it.available }

    /** Whether an option says if it was found there: not for [NONE], it is there whenever the hub takes host sets. */
    fun showsDetection(id: String): Boolean = id != NONE

    /** The detection line under an option; missing detection is not reported as "Not detected". */
    fun detection(id: String, machine: Machine): CoreText {
        if (!machine.online) return CoreText.of(Key.EngineOffline)
        val found = machine.engines.firstOrNull { it.id == id } ?: return CoreText.of(Key.EngineUnknown)
        return when {
            !found.available -> CoreText.of(Key.EngineMissing)
            !found.version.isNullOrBlank() -> CoreText.of(Key.EngineFoundVersion, found.version.orEmpty())
            else -> CoreText.of(Key.EngineFound)
        }
    }

    /**
     * The note under the options, for the option [selected] (the machine's own until something else is picked). On a [isSet]
     * set it says where new sessions go when the pick stays inside it, and that the others are turned off when it does not.
     */
    fun footer(machine: Machine, selected: String = current(machine)): CoreText = when {
        selected == NONE -> CoreText.of(if (machine.online) Key.EngineFooterNone else Key.EngineFooterNoneOffline)
        keepsSet(selected, machine) -> CoreText.of(if (machine.online) Key.EngineFooterSet else Key.EngineFooterSetOffline, name(selected, machine))
        isSet(machine) -> CoreText.of(if (machine.online) Key.EngineFooterSetOne else Key.EngineFooterSetOneOffline, name(selected, machine))
        machine.online -> CoreText.of(Key.EngineFooter)
        else -> CoreText.of(Key.EngineFooterOffline)
    }

    /** What "Save" says once the hub took the choice: a new default when the set stayed ([save]), else a switch. */
    fun saved(machine: Machine, save: Save? = null): CoreText = CoreText.of(
        when {
            !machine.online -> Key.EngineSavedOffline
            save is Save.Default -> Key.EngineDefaultChanged
            else -> Key.EngineSwitched
        },
    )
}
