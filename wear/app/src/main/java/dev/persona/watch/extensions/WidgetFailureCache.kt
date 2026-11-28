package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig

/** Discovery failures revoke a Hub catalogue; an individual projection failure has narrower scope. */
internal object WidgetFailureCache {
    fun invalidates(problem: ExtensionProblem?) = problem in setOf(
        ExtensionProblem.AUTHENTICATION, ExtensionProblem.FORBIDDEN, ExtensionProblem.UNAVAILABLE)

    fun invalidate(store: ExtensionCacheStore, group: String, config: HubConfig, problem: ExtensionProblem?,
                   widget: ExtensionWidget? = null, family: String? = null) {
        if (!invalidates(problem)) return
        if (problem == ExtensionProblem.UNAVAILABLE && widget != null && family != null)
            store.invalidateProjection(group, config, widget, family)
        else store.invalidate(group, config)
    }
}
