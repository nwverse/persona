package persona.feature.inbox

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import persona.core.contracts.api.ManagerGroup
import persona.core.contracts.api.ManagerRoster
import persona.core.designsystem.*

// "Rules" and "Muted" with several managers (docs/assistant/managers.md "Grouping"; the web's RulesDialog and MutesDialog):
// each manager keeps its own, so each one's list is read with it named and shown under its name, the default first, and
// what is changed there goes to it. One that can't be read says so in its place.

/** One manager's share of "Rules" or "Muted": its entries ([group]), or why they couldn't be read ([failed]). */
data class ManagerListShare<T>(val group: ManagerGroup<T>, val failed: Throwable? = null) {
    /** Worth a place on the page: entries, or a failure to say. */
    val shown: Boolean get() = failed != null || group.items.isNotEmpty()

    fun withItems(change: (List<T>) -> List<T>) = copy(group = group.copy(items = change(group.items)))
}

/** What the lists read from each manager follow: which are installed and online, and which is the default; null with one. */
internal fun ManagerRoster?.readKey(): List<String>? =
    this?.takeIf { it.isMulti }?.let { r -> listOf(r.defaultManagerId) + r.installed.map { "${it.id}:${it.online}" } }

/** [read] (by manager id) asked of every installed manager of [roster] at once, the default first. */
internal suspend fun <T> readEach(roster: ManagerRoster, read: suspend (String) -> List<T>): List<ManagerListShare<T>> = coroutineScope {
    roster.installed.map { m ->
        async {
            try { ManagerListShare(ManagerGroup(m.id, m, read(m.id))) }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { ManagerListShare(ManagerGroup(m.id, m, emptyList<T>()), e) }
        }
    }.awaitAll()
}

/**
 * The shares with something to show, each under its manager's name: its rows, else why it couldn't be read ([offline]
 * while that manager is offline, [said] otherwise).
 */
@Composable
internal fun <T> ManagerListShares(
    shares: List<ManagerListShare<T>>, @StringRes offline: Int, said: @Composable (Throwable) -> String, row: @Composable (T) -> Unit,
) {
    shares.filter { it.shown }.forEach { share ->
        val failed = share.failed
        JManagerHeader(share.group, count = share.group.items.size.takeIf { failed == null })
        if (failed == null) JGroup(rows = share.group.items.map { item -> { row(item) } })
        else Text(if (share.group.manager?.online == false) stringResource(offline) else said(failed), Modifier.padding(horizontal = rowPadding),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
    }
}
