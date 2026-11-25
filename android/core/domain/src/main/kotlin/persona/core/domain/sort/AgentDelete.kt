package persona.core.domain.sort

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.Session

/**
 * What "Delete conversation" asks, the same from the Agents list and the chat's menu (the web's DeleteSessionDialog, iOS
 * DeleteSessionText): it closes where supported and leaves every app's list; the record stays on the computer.
 */
fun Session.deleteConfirmation(machineName: String?): CloseConfirmation {
    val machine = machineName?.takeIf(String::isNotBlank)
    // The command alone ("claude --resume"), without the directory and id.
    val command = resumeCommand(cwd = "")?.substringBeforeLast(' ')
    val message = when {
        machine != null && command != null -> CoreText.of(Key.DeleteSessionOnMachineCommand, displayTitle, machine, command)
        machine != null -> CoreText.of(Key.DeleteSessionOnMachine, displayTitle, machine)
        command != null -> CoreText.of(Key.DeleteSessionCommand, displayTitle, command)
        else -> CoreText.of(Key.DeleteSession, displayTitle)
    }
    return CloseConfirmation(CoreText.of(Key.DeleteSessionTitle),
        CoreText.joined("\n\n", listOf(message, CoreText.of(Key.DeleteSessionClosesAgent))))
}
