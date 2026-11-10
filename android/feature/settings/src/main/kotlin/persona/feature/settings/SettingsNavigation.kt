package persona.feature.settings

import persona.core.navigation.SettingsPage

/** Stable page names shared by standalone settings and the navigation shell. */
fun SettingsPage.toInitialPage(): String = when (this) {
    SettingsPage.Diagnostics -> "diag"
    SettingsPage.Readiness -> "ready"
    SettingsPage.Notifications -> "notify"
    SettingsPage.PhoneData -> "phone"
    SettingsPage.Appearance -> "style"
    SettingsPage.Devices -> "devices"
    SettingsPage.About -> "about"
    SettingsPage.Pairing -> "pair"
    SettingsPage.ClaudeCode -> "claude"
    SettingsPage.ModelConfig -> "models"
    SettingsPage.Hubs -> "hubs"
    SettingsPage.Persona -> "persona"
    SettingsPage.Extensions -> "plugins"
    SettingsPage.Mail -> "mail"
    SettingsPage.Feishu -> "feishu"
    SettingsPage.Channels -> "channels"
    SettingsPage.HubUpdate -> "hub-update"
    SettingsPage.Translation -> "translate"
    SettingsPage.Triage -> "triage"
    SettingsPage.Capsule -> "capsule"
    SettingsPage.Language -> "language"
    SettingsPage.Codex -> "codex"
    SettingsPage.Providers -> "providers"
    SettingsPage.Storage -> "storage"
    SettingsPage.Desktops -> "desktops"
    SettingsPage.Managers -> "managers"
    SettingsPage.FeishuBot -> "feishu-bot"
    SettingsPage.Sent -> "connectors/sent"
    SettingsPage.Connectors -> "connectors"
}

/** The legacy ledger page opens at its new location; the root and unknown names have no destination. */
fun settingsPageFromInitialPage(initialPage: String): SettingsPage? =
    if (initialPage == "sent") SettingsPage.Sent
    else SettingsPage.entries.firstOrNull { it.toInitialPage() == initialPage }

/**
 * The pages of Settings' first group, the current assistant's settings (docs/assistant/managers.md "Clients"), and the
 * pages they open: what a switch of assistant closes, so no page shows one assistant's settings once another is current.
 */
private val assistantPages = setOf(SettingsPage.Providers, SettingsPage.ModelConfig, SettingsPage.Triage, SettingsPage.Connectors,
    SettingsPage.Mail, SettingsPage.Feishu, SettingsPage.FeishuBot, SettingsPage.Sent, SettingsPage.Extensions, SettingsPage.Channels,
    SettingsPage.ClaudeCode, SettingsPage.Codex)

/** Whether [page] (an in-place page key, [SettingsPage.toInitialPage]) is one of the current assistant's settings pages. */
internal fun isAssistantSettingsPage(page: String): Boolean =
    page.startsWith("connector/") || page == "mail-add" || settingsPageFromInitialPage(page)?.let { it in assistantPages } == true

/** Whether the settings destination [key] is one of the current assistant's (its pages and what they open). */
fun isAssistantSettingsDestination(key: androidx.navigation3.runtime.NavKey): Boolean = when (key) {
    is persona.core.navigation.SettingsPageRoute -> key.page in assistantPages
    persona.core.navigation.AssistantSettingsRoute, is persona.core.navigation.ModelPurposeRoute, is persona.core.navigation.ModelProviderRoute,
    is persona.core.navigation.ExtensionRoute, is persona.core.navigation.ExtensionUiRoute, persona.core.navigation.ExtensionMarketRoute,
    is persona.core.navigation.ExtensionCatalogRoute, persona.core.navigation.ExtensionMarketsRoute, is persona.core.navigation.ConnectorRoute,
    is persona.core.navigation.ChannelRoute -> true
    else -> false
}
