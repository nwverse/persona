package dev.persona.watch.extensions

/** The picker publishes a discovered catalogue together with the client that authorized it. */
internal class WidgetConfigCatalog {
    class Loaded private constructor(
        val client: ExtensionClient,
        val allWidgets: List<ExtensionWidget>,
        val widgets: List<ExtensionWidget>,
        val loadedAtMs: Long,
    ) {
        companion object {
            fun discover(client: ExtensionClient, kind: WidgetFaceKind, now: () -> Long = System::currentTimeMillis): Loaded {
                val found = client.widgets().toList()
                return Loaded(client, found, found.filter { it.familyFor(kind) != null }, now())
            }
        }
    }

    private var loaded: Loaded? = null
    val widgets: List<ExtensionWidget> get() = loaded?.widgets.orEmpty()

    /** Call when leaving a connection or starting any catalogue refresh, including preview Back. */
    fun clear() { loaded = null }
    fun publish(next: Loaded) { loaded = next }

    fun clientFor(widget: ExtensionWidget, now: Long = System.currentTimeMillis()): ExtensionClient? {
        val current = loaded ?: return null
        if (now < current.loadedAtMs || now - current.loadedAtMs !in 0L until CachedWidget.MAX_AGE_MS) return null
        return current.client.takeIf { widget in current.widgets && it.canProject(widget) }
    }
}
