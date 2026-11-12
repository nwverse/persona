package persona.feature.chat

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import persona.core.domain.tasks.BackgroundRound

/**
 * The hub's "Fold background rounds" switch (#4314; on until read and on a hub without it) and the main conversation.
 * [reviews]: the background supervisor's reviews stay in the main conversation (#5084); null while none is in effect
 * and on a hub without it.
 */
internal data class BackgroundFoldSetting(
    val on: Boolean = true, val main: String? = null, val reviews: persona.core.domain.tasks.BackgroundReviews? = null,
) {
    companion object {
        /**
         * The last value read this run, by conversation (in memory only): until the hub answers again the page draws
         * with it, so a user who turned folding off does not see a round fold for a moment.
         */
        private val lastRead = java.util.concurrent.ConcurrentHashMap<String, BackgroundFoldSetting>()

        fun lastKnown(conversation: String): BackgroundFoldSetting = lastRead[conversation] ?: BackgroundFoldSetting()
        fun store(conversation: String, setting: BackgroundFoldSetting) { lastRead[conversation] = setting }
    }
}

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
