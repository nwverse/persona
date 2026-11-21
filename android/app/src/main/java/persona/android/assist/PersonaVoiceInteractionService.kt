package persona.android.assist

import android.os.Bundle
import android.service.voice.VoiceInteractionService
import android.service.voice.VoiceInteractionSession

/**
 * Registers Persona as an Android Default Digital Assistant (`ROLE_ASSISTANT`), compatible with
 * system gesture triggers and Samsung One UI side-button long-press (including from the keyguard).
 */
class PersonaVoiceInteractionService : VoiceInteractionService() {
    override fun onLaunchVoiceAssistFromKeyguard() {
        runCatching {
            showSession(
                Bundle(),
                VoiceInteractionSession.SHOW_WITH_ASSIST or VoiceInteractionSession.SHOW_WITH_SCREENSHOT,
            )
        }
    }
}
