package persona.feature.inbox

import android.content.res.Resources
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import java.time.Instant
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.api.InboxParentRef
import persona.core.contracts.parseTime
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.JAction
import persona.core.designsystem.JActionSheet
import persona.core.designsystem.JGroupItem
import persona.core.designsystem.JIconButton
import persona.core.designsystem.JLoading
import persona.core.designsystem.JNotice
import persona.core.designsystem.JRow
import persona.core.designsystem.JSearchField
import persona.core.designsystem.JSheet
import persona.core.designsystem.JTextButton
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons

/** Open items this one can join: same manager, not itself or its direct children, newest first, title / where search. */
internal fun sameMatterCandidates(
    records: List<InboxRecord>, current: InboxRecord, children: List<InboxRecord>, query: String,
    where: (InboxRecord) -> String? = ::sameMatterWhere,
): List<InboxRecord> {
    val excluded = buildSet { add(current.item.id); children.mapTo(this) { it.item.id } }
    val manager = InboxItemKey.appId("", current.manager)
    val q = query.trim()
    return records.filter { candidate ->
        !candidate.item.isDone && InboxItemKey.appId("", candidate.manager) == manager && candidate.item.id !in excluded &&
            (q.isEmpty() || candidate.item.title.contains(q, ignoreCase = true) || where(candidate)?.contains(q, ignoreCase = true) == true)
    }.map { it to parseTime(it.item.updatedAt) }
        .sortedWith(compareByDescending<Pair<InboxRecord, Instant?>> { it.second }.thenBy { it.first.item.id })
        .map { it.first }
}

/** The display package's `where` field replaces this placeholder when its baseline lands; title search works today. */
internal fun sameMatterWhere(record: InboxRecord): String? = null

/** The legacy row's sender and latest words. The shared display row meta wraps this once `where` is available. */
internal fun sameMatterLegacyMeta(record: InboxRecord, res: Resources): String? {
    val sender = record.item.sender?.takeIf { it.isNotBlank() }
    val preview = record.item.preview.takeIf { it.isNotBlank() }
    return when {
        sender == null -> preview
        preview == null -> sender
        else -> res.getString(R.string.inbox_line_legacy_meta, sender, preview)
    }
}

internal fun sameMatterMeta(record: InboxRecord, res: Resources): String? = sameMatterLegacyMeta(record, res)

/** Pick another open item as this item's parent. The tree is read only when the caller did not already have one. */
@Composable
internal fun InboxSameMatterSheet(
    repository: InboxRepository,
    record: InboxRecord,
    initialTree: InboxLineage?,
    onDismiss: () -> Unit,
    onLinked: (InboxLineage?) -> Unit,
) {
    val snapshot by repository.snapshot.collectAsStateWithLifecycle()
    val feedback = LocalAppFeedback.current
    val res = LocalResources.current
    val scope = rememberCoroutineScope()
    var query by rememberSaveable(record.item.id) { mutableStateOf("") }
    var tree by remember(record.item.id) { mutableStateOf(initialTree) }
    var loading by remember(record.item.id) { mutableStateOf(initialTree == null) }
    var failed by remember(record.item.id) { mutableStateOf(false) }
    var attempt by remember(record.item.id) { mutableIntStateOf(0) }
    var busy by remember(record.item.id) { mutableStateOf(false) }
    var writeFailed by remember(record.item.id) { mutableStateOf(false) }
    LaunchedEffect(record.item.id, initialTree, attempt) {
        if (initialTree != null) {
            tree = initialTree; loading = false; failed = false
            return@LaunchedEffect
        }
        loading = true; failed = false
        tree = try {
            repository.inboxLine(record) ?: InboxLineage(InboxParentRef("inbox", record.hubItemId), record)
        } catch (e: CancellationException) { throw e }
        catch (_: Exception) { null }
        loading = false
        failed = tree == null
    }
    val candidates = sameMatterCandidates(snapshot.records, record, tree?.children.orEmpty(), query)
    JSheet(onDismiss = onDismiss, title = stringResource(R.string.inbox_line_same), dismissible = !busy) {
        Column(Modifier.fillMaxWidth().fillMaxHeight(0.92f).imePadding()) {
            JSearchField(query, { query = it }, stringResource(R.string.inbox_line_search_placeholder), Modifier.fillMaxWidth())
            if (writeFailed) JNotice(stringResource(R.string.inbox_failed_retry), Modifier.padding(top = Tokens.Space.s))
            when {
                loading -> Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) { JLoading() }
                failed -> Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
                    JNotice(stringResource(R.string.inbox_refresh_failed), actionLabel = stringResource(R.string.inbox_retry), onAction = { attempt++ })
                }
                candidates.isEmpty() -> Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
                    Text(stringResource(R.string.inbox_line_empty), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink3)
                }
                else -> LazyColumn(Modifier.weight(1f).fillMaxWidth()) {
                    itemsIndexed(candidates, key = { _, candidate -> candidate.item.id }) { index, candidate ->
                        JGroupItem(GroupPosition.of(index, candidates.size)) {
                            JRow(candidate.item.title, subtitle = sameMatterMeta(candidate, res), enabled = !busy, onClick = {
                                busy = true; writeFailed = false
                                scope.launch {
                                    try {
                                        repository.setInboxParent(record, InboxParentRef("inbox", candidate.hubItemId))
                                        // As after unlinking, a failed reread must not turn a completed write into a failure.
                                        val fresh = try { repository.inboxLine(record) }
                                        catch (e: CancellationException) { throw e }
                                        catch (_: Exception) { null }
                                        onDismiss()
                                        onLinked(fresh)
                                        feedback?.notice(res.getString(R.string.inbox_line_linked, candidate.item.title), FeedbackStyle.Success)
                                    } catch (e: CancellationException) { throw e }
                                    catch (e: Exception) { if (feedback?.failure(e, operation = true) != true) writeFailed = true }
                                    finally { busy = false }
                                }
                            })
                        }
                    }
                }
            }
            JTextButton(stringResource(persona.core.designsystem.R.string.designsystem_cancel), onDismiss,
                Modifier.align(Alignment.End).padding(top = Tokens.Space.s), enabled = !busy)
        }
    }
}

/** The event page's More menu. It opens the picker lazily, including when this item has no visible line. */
@Composable
internal fun InboxSameMatterMenu(repository: InboxRepository, record: InboxRecord, onLinked: () -> Unit) {
    var menu by remember(record.item.id) { mutableStateOf(false) }
    var showSame by remember(record.item.id) { mutableStateOf(false) }
    JIconButton(PersonaIcons.more, stringResource(R.string.inbox_more), onClick = { menu = true })
    if (menu) JActionSheet(title = record.item.title, onDismiss = { menu = false }, actions = listOf(
        JAction(stringResource(R.string.inbox_line_same), PersonaIcons.link, onClick = { showSame = true }),
    ))
    if (showSame) InboxSameMatterSheet(repository, record, initialTree = null,
        onDismiss = { showSame = false }, onLinked = { onLinked() })
}
