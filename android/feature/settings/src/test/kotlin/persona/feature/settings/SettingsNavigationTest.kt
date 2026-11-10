package persona.feature.settings

import persona.core.navigation.AssistantSettingsRoute
import persona.core.navigation.ConnectorRoute
import persona.core.navigation.ManagerRoute
import persona.core.navigation.ModelPurposeRoute
import persona.core.navigation.SettingsPage
import persona.core.navigation.SettingsPageRoute
import persona.core.navigation.SettingsRoute
import org.junit.Assert.*
import org.junit.Test

class SettingsNavigationTest {
    @Test fun everyDestinationKeepsItsExistingPageNameAndRoundTrips() {
        val pages = mapOf(
            SettingsPage.Diagnostics to "diag",
            SettingsPage.Readiness to "ready",
            SettingsPage.Notifications to "notify",
            SettingsPage.PhoneData to "phone",
            SettingsPage.Appearance to "style",
            SettingsPage.Devices to "devices",
            SettingsPage.About to "about",
            SettingsPage.Pairing to "pair",
            SettingsPage.ClaudeCode to "claude",
            SettingsPage.ModelConfig to "models",
            SettingsPage.Hubs to "hubs",
            SettingsPage.Persona to "persona",
            SettingsPage.Extensions to "plugins",
            SettingsPage.Mail to "mail",
            SettingsPage.Feishu to "feishu",
            SettingsPage.Channels to "channels",
            SettingsPage.HubUpdate to "hub-update",
            SettingsPage.Translation to "translate",
            SettingsPage.Triage to "triage",
            SettingsPage.Capsule to "capsule",
            SettingsPage.Language to "language",
            SettingsPage.Codex to "codex",
            SettingsPage.Providers to "providers",
            SettingsPage.Storage to "storage",
            SettingsPage.Desktops to "desktops",
            SettingsPage.Managers to "managers",
            SettingsPage.FeishuBot to "feishu-bot",
            SettingsPage.Sent to "connectors/sent",
            SettingsPage.Connectors to "connectors",
        )
        assertEquals(SettingsPage.entries.toSet(), pages.keys)
        assertEquals(pages.size, pages.values.toSet().size)
        pages.forEach { (page, name) ->
            assertEquals(name, page.toInitialPage())
            assertEquals(page, settingsPageFromInitialPage(name))
            assertEquals(page, settingsPageFromInitialPage(page.toInitialPage()))
        }
    }

    @Test fun legacySentPageNamesResolveToTheNestedLedger() {
        assertEquals(SettingsPage.Sent, settingsPageFromInitialPage("sent"))
        assertEquals("connectors/sent", settingsPageFromInitialPage("sent")!!.toInitialPage())
    }

    @Test fun rootAndUnknownNamesHaveNoDetailDestination() {
        listOf("settings", "assistant", "", "unknown", "DIAG").forEach { assertNull(settingsPageFromInitialPage(it)) }
    }

    /** What a switch of assistant closes: the first group's pages and what they open, never this device's or the hub's. */
    @Test fun aSwitchClosesOnlyTheCurrentAssistantsPages() {
        val assistants = listOf(SettingsPage.Providers, SettingsPage.ModelConfig, SettingsPage.Triage, SettingsPage.Connectors,
            SettingsPage.Mail, SettingsPage.Feishu, SettingsPage.FeishuBot, SettingsPage.Sent, SettingsPage.Extensions,
            SettingsPage.Channels, SettingsPage.ClaudeCode, SettingsPage.Codex)
        SettingsPage.entries.forEach { page ->
            assertEquals(page.name, page in assistants, isAssistantSettingsDestination(SettingsPageRoute(page)))
            assertEquals(page.name, page in assistants, isAssistantSettingsPage(page.toInitialPage()))
        }
        listOf(AssistantSettingsRoute, ModelPurposeRoute("agent.triage", "m-2"), ConnectorRoute("github", "m-2"))
            .forEach { assertTrue("$it", isAssistantSettingsDestination(it)) }
        listOf(SettingsRoute, ManagerRoute("m-2")).forEach { assertFalse("$it", isAssistantSettingsDestination(it)) }
        assertTrue(isAssistantSettingsPage("connector/github"))
        assertTrue(isAssistantSettingsPage("mail-add"))
        assertFalse(isAssistantSettingsPage("settings"))
    }
}
