package persona.platform.notify

import persona.core.contracts.api.ExtensionManagement
import persona.core.contracts.api.ExtensionNotificationActionRecord
import persona.core.contracts.api.ExtensionNotificationScope
import persona.core.contracts.api.ExtensionUi
import persona.core.contracts.api.ManagerPins
import persona.core.contracts.api.pinned

/** Resolves only the currently selected, still-identical pairing; it never opens another hub connection. */
interface ExtensionNotificationEnvironment {
    fun currentScope(): ExtensionNotificationScope?
    fun extensionUi(record: ExtensionNotificationActionRecord): ExtensionUi?
}

fun ExtensionManagement.forExtensionNotification(record: ExtensionNotificationActionRecord): ExtensionUi {
    val pins = ManagerPins.of(listOf(listOf("extensions", record.extension), listOf("plugins", record.extension)), record.scope.manager)
    return pinned(pins)
}
