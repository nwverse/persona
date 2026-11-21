package dev.persona.watch.pet

/** Ambient display suppresses reads, but only leaving the foreground stops the polling chain. */
internal class PetForegroundPoll(
    private val foreground: () -> Boolean,
    private val interactive: () -> Boolean,
    private val refresh: () -> Unit,
    private val schedule: (Runnable, Long) -> Unit,
) : Runnable {
    override fun run() {
        if (!foreground()) return
        if (interactive()) refresh()
        if (foreground()) schedule(this, PetTokens.pollMs)
    }
}
