package persona.feature.chat

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import persona.core.domain.tasks.BackgroundRound

/** The hub's "Fold background rounds" switch (#4314; on until read and on a hub without it) and the main conversation. */
internal data class BackgroundFoldSetting(val on: Boolean = true, val main: String? = null)

/**
 * A folded background round of the main conversation (#4314, docs/assistant/assistant.md "Background rounds"): one muted
 * line, `@<source> · Background round` (`· Running in the background` while it runs), the same line as the work summary.
 * A tap opens it and its whole transcript follows it in place; another tap folds it again.
 */
@Composable
internal fun BackgroundRoundLine(round: BackgroundRound, onToggle: (String) -> Unit) {
    val mention = stringResource(R.string.chat_source_label, sourceTitle(round.source))
    val label = stringResource(if (round.live) R.string.chat_background_round_live else R.string.chat_background_round, mention)
    AssistantFoldLine(label, round.open, Modifier.testTag("background-round-${round.id}")) { onToggle(round.id) }
}
