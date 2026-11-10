package persona.feature.inbox

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler

/**
 * A link host (the app's ChatLinkHost) that opens a link as a manager's (docs/assistant/managers.md "Clients"): a Feishu
 * document or Moments post it shows natively is read through [openUri]'s manager rather than the host's own.
 */
interface ManagerUriHandler : UriHandler {
    fun openUri(uri: String, manager: String?)
}

/** [content]'s links open as [manager]'s (an inbox item's page); as they are for none, or a host that cannot. */
@Composable
fun ManagerLinks(manager: String?, content: @Composable () -> Unit) {
    val parent = LocalUriHandler.current
    val handler = remember(parent, manager) {
        if (manager == null || parent !is ManagerUriHandler) parent
        else object : UriHandler { override fun openUri(uri: String) = parent.openUri(uri, manager) }
    }
    CompositionLocalProvider(LocalUriHandler provides handler, content = content)
}
