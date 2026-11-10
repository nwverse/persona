package persona.core.navigation

import org.junit.Assert.*
import org.junit.Test

class CapsuleRouteTest {
    @Test fun opensTheContributingExtensionsPageWithItsState() {
        val destination = Router().parse(RouteInput.DeepLink("persona://open?target=extension&extension=sample&ui=details&title=Usage&state=next%2Fpage"))
        assertEquals(AppTab.Persona, destination?.tab)
        assertEquals(ExtensionUiRoute("sample", "details", "Usage", "next/page"), destination?.key)
    }
    @Test fun refusesMissingOrBlankExtensionIdentity() {
        listOf("target=extension&extension=sample", "target=extension&extension=&ui=details", "target=extension&extension=sample&ui=").forEach {
            assertNull(Router().parse(RouteInput.DeepLink("persona://open?$it")))
        }
    }
    @Test fun opensCapsuleSettingsThroughTheExistingSettingsRoute() {
        val destination = Router().parse(RouteInput.DeepLink("persona://open?target=settings&page=capsule"))
        assertEquals(SettingsPageRoute(SettingsPage.Capsule), destination?.key)
    }
}
