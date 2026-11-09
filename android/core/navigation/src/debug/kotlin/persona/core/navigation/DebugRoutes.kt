package persona.core.navigation

/** Only the app's debug source set may adapt Intent extras to this parser. */
fun Router.parseDebug(arguments: Map<String, String>): ResolvedRoute? {
    val selectors = listOf("debug.openTab", "debug.openSession", "debug.openRequest", "debug.openPairing", "debug.openDiagnostics")
    if (selectors.count { it in arguments } != 1) return null
    val kind = when (arguments["debug.conversationKind"]) {
        null -> null
        "assistant" -> ConversationKind.Assistant
        "agent" -> ConversationKind.Agent
        else -> return null
    }
    val key = when {
        "debug.openTab" in arguments -> linkedPage(arguments.getValue("debug.openTab")) ?: return null
        "debug.openSession" in arguments -> sessionPath(arguments.getValue("debug.openSession"), kind) ?: return null
        "debug.openRequest" in arguments -> InteractionRoute(
            sessionPath(arguments["debug.session"] ?: return null, kind) ?: return null,
            arguments.getValue("debug.openRequest"),
        )
        "debug.openPairing" in arguments -> {
            val value = arguments.getValue("debug.openPairing")
            if (listOf("persona://pair", "persona://pair").any(value::startsWith)) return parse(RouteInput.DeepLink(value))
            if (value != "true") return null
            PairingRoute
        }
        "debug.openDiagnostics" in arguments -> {
            if (arguments.getValue("debug.openDiagnostics") != "true") return null
            SettingsPageRoute(SettingsPage.Diagnostics)
        }
        else -> return null
    }
    return parse(RouteInput.Internal(key))
}
