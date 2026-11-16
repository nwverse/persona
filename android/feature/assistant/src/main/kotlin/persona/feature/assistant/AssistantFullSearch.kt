package persona.feature.assistant

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantManagement
import persona.core.contracts.api.AssistantSearchAnswer
import persona.core.designsystem.JEmptyState
import persona.core.designsystem.JFullScreen
import persona.core.designsystem.JIcon
import persona.core.designsystem.JIconButton
import persona.core.designsystem.JRow
import persona.core.designsystem.JSearchField
import persona.core.designsystem.JSectionHeader
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcons
import persona.core.navigation.MessageJumps
import java.time.Instant

/**
 * Whether the sidebar's Search opens over the whole window (a regular window, as the open iPhone Duo) or stays inline
 * in the compact drawer (#9171).
 */
internal fun sidebarOpensFullSearch(regularWindow: Boolean): Boolean = regularWindow

/**
 * Open / close / pick for the full-window search: opening is only for a regular window; Back, Close and a row all
 * dismiss it.
 */
internal data class FullSearchGate(val open: Boolean = false) {
    fun onSearch(regularWindow: Boolean): FullSearchGate =
        if (regularWindow) copy(open = true) else this

    fun dismiss(): FullSearchGate = copy(open = false)

    fun afterPick(): FullSearchGate = copy(open = false)
}

/**
 * Persona's chats for the full-window search, latest first. A blank query lists them all; otherwise title, last
 * message and a direction's name match (the same local filter the sidebar uses).
 */
internal fun filterChatsForFullSearch(
    chats: List<Session>,
    query: String,
    words: ConversationWords,
    names: Map<String, String> = emptyMap(),
    backgroundId: String? = null,
): List<Session> {
    val ordered = chats.distinctBy { it.key }
        .map { it to it.updated }
        .sortedWith(compareByDescending<Pair<Session, Instant?>> { it.second }.thenBy { it.first.key.path })
        .map { it.first }
    val q = query.trim()
    if (q.isEmpty()) return ordered
    return ordered.filter {
        it.conversationTitle(words).contains(q, ignoreCase = true) ||
            it.lastText.orEmpty().contains(q, ignoreCase = true) ||
            names[it.sessionId].orEmpty().contains(q, ignoreCase = true) ||
            (it.sessionId == backgroundId && words.background.contains(q, ignoreCase = true))
    }
}

/**
 * Rows the full-window search lists: local matches at once (latest first), and when the hub lists assistant search the
 * manager's answer merges in the same way as the sidebar ([mergeAssistantSearch]).
 */
internal fun fullSearchRows(
    query: String,
    chats: List<Session>,
    known: List<Session>,
    enabled: Boolean,
    answer: AssistantSearchAnswer?,
    words: ConversationWords,
    names: Map<String, String> = emptyMap(),
    backgroundId: String? = null,
): List<FoundChat> {
    val local = filterChatsForFullSearch(chats, query, words, names, backgroundId)
    if (!enabled || query.trim().isEmpty()) return local.map { FoundChat(it, null) }
    return mergeAssistantSearch(local, answer, known)
}

/**
 * Search over the whole window on a regular window (#9171), as the open iPhone Duo's chats-only full search: the field
 * at the foot above the keyboard, a close button beside it, Persona's chats above (latest first), filtered as typed,
 * including the manager search when the hub lists it. A row opens its chat and dismisses; Back and Close dismiss too.
 */
@Composable
internal fun AssistantFullSearch(
    chats: List<Session>,
    known: List<Session>,
    searchOn: Boolean,
    management: AssistantManagement?,
    hubKey: String,
    words: ConversationWords,
    names: Map<String, String> = emptyMap(),
    backgroundId: String? = null,
    mainName: String? = null,
    mainKey: SessionKey? = null,
    onOpen: (SessionKey) -> Unit,
    onDismiss: () -> Unit,
    /** Seeded for JVM shots; production leaves it blank. */
    initialQuery: String = "",
    /** False draws the page in place (JVM shots); production keeps the full-screen dialog. */
    overlay: Boolean = true,
) {
    var query by rememberSaveable { mutableStateOf(initialQuery) }
    val (found, partial) = rememberFoundChats(
        query,
        remember(chats, query, words, names, backgroundId) {
            filterChatsForFullSearch(chats, query, words, names, backgroundId)
        },
        known, searchOn, management, hubKey,
    )
    val rows = remember(query, chats, known, searchOn, found, words, names, backgroundId) {
        // rememberFoundChats returns null while the feature is off or the query is blank: fall back to the local list.
        found ?: fullSearchRows(query, chats, known, enabled = false, answer = null, words, names, backgroundId)
    }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        runCatching { focus.requestFocus() }
    }
    val body: @Composable () -> Unit = {
        Column(
            Modifier
                .fillMaxSize()
                .background(PersonaTheme.palette.background)
                .then(if (overlay) Modifier.statusBarsPadding().imePadding() else Modifier)
                .testTag("full-search"),
        ) {
            val heading = if (query.isBlank()) {
                stringResource(R.string.assistant_recent)
            } else {
                pluralStringResource(R.plurals.assistant_full_search_chats, rows.size, rows.size)
            }
            LazyColumn(
                Modifier.weight(1f).fillMaxWidth().testTag("full-search-list"),
                contentPadding = PaddingValues(
                    start = Tokens.Space.page, end = Tokens.Space.page, bottom = Tokens.Space.m,
                ),
            ) {
                item { JSectionHeader(heading) }
                if (partial) item {
                    Text(
                        stringResource(R.string.assistant_search_partial),
                        Modifier.padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
                        style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2,
                    )
                }
                items(rows, key = { it.session.key.path }) { row ->
                    FullSearchRow(
                        row = row,
                        title = when {
                            mainKey != null && row.session.key == mainKey && !mainName.isNullOrBlank() -> mainName
                            else -> names[row.session.sessionId] ?: row.session.conversationTitle(words)
                        },
                        onOpen = {
                            row.hit?.let { MessageJumps.request(row.session.machineId, row.session.sessionId, it.itemId) }
                            onOpen(row.session.key)
                        },
                    )
                }
                if (rows.isEmpty() && query.isNotBlank()) item {
                    JEmptyState(
                        stringResource(R.string.assistant_full_search_no_results, query),
                        stringResource(R.string.assistant_no_match_detail),
                        icon = PhIcons.MagnifyingGlass,
                    )
                }
            }
            Row(
                Modifier
                    .fillMaxWidth()
                    .then(if (overlay) Modifier.navigationBarsPadding() else Modifier)
                    .padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.s),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                JSearchField(
                    query, { query = it }, stringResource(R.string.assistant_search_chats),
                    Modifier.weight(1f).focusRequester(focus).testTag("full-search-field"),
                )
                Spacer(Modifier.width(Tokens.Space.m))
                JIconButton(
                    PersonaIcons.close, stringResource(persona.core.designsystem.R.string.designsystem_close),
                    onDismiss, raised = true, modifier = Modifier.testTag("full-search-close"),
                )
            }
        }
    }
    if (overlay) JFullScreen(onDismiss, body) else body()
}

@Composable
private fun FullSearchRow(row: FoundChat, title: String, onOpen: () -> Unit) {
    val said = row.hit?.let(::markedSnippet)
    val preview = row.session.lastText?.replace("\n", " ")?.takeIf { it.isNotBlank() }
    JRow(
        title,
        onClick = onOpen,
        subtitle = if (said == null) preview else null,
        subtitleMark = said,
        chevron = false,
        leading = {
            Box(
                Modifier
                    .size(Tokens.MinTouch.android)
                    .clip(RoundedCornerShape(Tokens.Radius.control))
                    .background(PersonaTheme.palette.surfaceStrong),
                contentAlignment = Alignment.Center,
            ) {
                JIcon(PersonaIcons.conversation, null, size = Tokens.Size.iconXS, tint = PersonaTheme.palette.ink2)
            }
        },
    )
}
