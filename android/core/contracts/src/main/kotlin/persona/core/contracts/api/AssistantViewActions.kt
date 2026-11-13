package persona.core.contracts.api

import persona.core.contracts.generated.ExtensionAssistantView
import persona.core.contracts.generated.ExtensionViewAction

// A card's answer on the hub (docs/assistant/assistant-views.md "Persistent submission"). The requests are the generated
// ExtensionViewActionInput / ExtensionAssistantHTMLActionInput; this is what the app makes of the answers.

/** What the hub answered a card's action. Any other refusal is thrown as the hub's failure. */
sealed interface AssistantViewAnswer {
    /** Recorded (or the same request again): the card's answer as the hub keeps it. */
    data class Recorded(val action: ExtensionViewAction) : AssistantViewAnswer

    /** 409 `already_answered`: another answer was recorded first (on this device or another), here it is. */
    data class AlreadyAnswered(val action: ExtensionViewAction?) : AssistantViewAnswer

    /** 409 `view_changed`: the card is no longer the version the user saw; [view] is the current one when sent. */
    data class ViewChanged(val version: Long?, val view: ExtensionAssistantView?) : AssistantViewAnswer

    /** An HTML page's `open`, checked by the hub: open [url] in the app if the card is still [version]. */
    data class Open(val url: String, val version: Long) : AssistantViewAnswer
}
