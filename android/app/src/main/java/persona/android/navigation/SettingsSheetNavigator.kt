package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import persona.core.navigation.*
import persona.platform.phonetools.PhoneToolsRoute

/** Settings has a secondary stack inside its sheet, never a third primary destination. */
internal class SettingsSheetNavigator(
    private val stack: MutableList<NavKey>,
    private val close: () -> Unit,
    private val outside: Navigator,
) : Navigator {
    override fun selectTab(tab: AppTab) { close(); outside.selectTab(tab) }

    override fun navigate(key: NavKey) {
        val destination = if (key == ConnectionSettingsRoute) SettingsPageRoute(SettingsPage.Diagnostics) else key
        when (destination) {
            SettingsRoute, AssistantSettingsRoute, is SettingsPageRoute, PairingRoute, PhoneToolsRoute,
            is EngineRoute, is ModelPurposeRoute, is ModelProviderRoute, is ManagerRoute,
            is ExtensionRoute, is ExtensionUiRoute, ExtensionMarketRoute, ExtensionMarketsRoute,
            is ExtensionCatalogRoute, is ConnectorRoute, is ChannelRoute, is LauncherRoute -> {
                if (stack.lastOrNull() != destination) stack.add(destination)
                restoreSecondaryParents(stack)
            }
            else -> { close(); outside.navigate(destination) }
        }
    }

    override fun back() {
        if (stack.size > 1) stack.removeAt(stack.lastIndex) else close()
    }
}
