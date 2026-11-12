package persona.feature.chat

import android.content.res.Resources
import androidx.compose.runtime.Immutable
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import persona.core.contracts.Item
import persona.core.contracts.Session
import persona.core.contracts.SessionKey

/**
 * The composer's @ menu (iOS `PromptItems.files` / `sessions`, web `lib/mentions.ts`): files this chat's tools read or
 * wrote (picking one types `@path`, which Claude Code and Codex read as that file), then other agents' sessions
 * (picking one quotes it as a `<persona-context>` before the message, not as text). In Persona's conversations the
 * conversation's open tasks come first (iOS `TaskMentionList`): picking one routes the next message to it.
 */
@Immutable
data class ComposerMentions(
    /** Absolute paths, newest first ([chatFiles]). */
    val files: List<String>,
    /** The session's folder: a file inside it is typed relative to it. */
    val cwd: String,
    /** Every session the list knows; [otherSessions] picks the ones to offer. */
    val sessions: List<Session>,
    /** This chat, left out of the sessions. */
    val except: SessionKey?,
    /** The conversation's open tasks ([mentionTasks]); null outside Persona's conversations. */
    val tasks: List<MentionTask>? = null,
)

/** One of the conversation's tasks in the @ menu: its [status] dot, title and where it runs ([place]). */
@Immutable
data class MentionTask(val id: String, val title: String, val place: String, val status: persona.core.designsystem.Status)

/** The `@word` being typed just before the caret: [start] is the `@`, [end] the caret. */
internal data class MentionToken(val start: Int, val end: Int, val query: String)

private val mentionBefore = Regex("(^|\\s)@([^\\s@]*)$")

/** The `@word` just before [caret]: at the start or after whitespace, so an e-mail address is not one. */
internal fun mentionAt(text: String, caret: Int): MentionToken? {
    if (caret < 0 || caret > text.length) return null
    val before = text.take(caret)
    val match = mentionBefore.find(before) ?: return null
    val query = match.groupValues[2]
    return MentionToken(before.length - query.length - 1, caret, query)
}

/** [value] with [token] replaced by [insert], the caret after it; a picked file ends in a space, never doubled. */
internal fun replaceMention(value: TextFieldValue, token: MentionToken, insert: String): TextFieldValue {
    val before = value.text.take(token.start)
    var after = value.text.drop(token.end)
    if (insert.endsWith(" ") && after.startsWith(" ")) after = after.drop(1)
    return TextFieldValue(before + insert + after, TextRange(before.length + insert.length))
}

/** [value] with `@` typed at the caret (a space first when it follows a word): the @ button. */
internal fun typeMention(value: TextFieldValue): TextFieldValue {
    val at = value.selection.min.coerceIn(0, value.text.length)
    val before = value.text.take(at)
    val typed = if (before.isNotEmpty() && !before.last().isWhitespace()) " @" else "@"
    return TextFieldValue(before + typed + value.text.drop(value.selection.max.coerceIn(at, value.text.length)), TextRange(at + typed.length))
}

/** A path as typed after @: relative to the session's folder when inside it. */
internal fun relativeMentionPath(path: String, cwd: String): String {
    val base = cwd.trimEnd('/')
    return if (base.isNotEmpty() && path.startsWith("$base/")) path.drop(base.length + 1) else path
}

/** Files the chat's tools read or wrote, newest first, at most [limit]; absolute paths. */
internal fun chatFiles(items: List<Item>, limit: Int = 12): List<String> {
    val out = LinkedHashSet<String>()
    for (item in items.asReversed()) {
        if (out.size >= limit) break
        if (item.kind != "tool_use") continue
        val input = item.toolInput as? JsonObject ?: continue
        val path = ((input["file_path"] ?: input["notebook_path"]) as? JsonPrimitive)?.takeIf { it.isString }?.content
        if (!path.isNullOrBlank()) out += path
    }
    return out.toList()
}

/** Other agents' sessions matching [query] (title or agent · folder), most recently active first, at most [limit]. */
internal fun otherSessions(all: List<Session>, except: SessionKey?, query: String, limit: Int = 8): List<Session> {
    val q = query.lowercase()
    return all.asSequence().filter { !it.assistant && it.key != except }
        .filter { q.isEmpty() || it.displayTitle.lowercase().contains(q) || sessionMentionDetail(it).lowercase().contains(q) }
        .sortedByDescending { it.updated }.take(limit).toList()
}

internal fun sessionMentionDetail(session: Session): String = listOf(session.agentLabel, session.folderName).filter(String::isNotEmpty).joinToString(" · ")

/** One row of the @ menu: a task to route the next message to, a file to type, or a session to quote. */
internal sealed interface MentionRow {
    val key: String
    data class Task(val task: MentionTask, override val key: String = "task:${task.id}") : MentionRow
    data class File(val path: String, val insert: String, override val key: String = "file:$path") : MentionRow
    data class Quote(val session: Session, override val key: String = "session:${session.key.path}") : MentionRow
}

/**
 * The @ menu for [query]: the tasks whose title contains it, the files whose relative path does, then the sessions it
 * matches. iOS lists every task whatever follows the @ in a panel of its own; here they share the files' list, so the
 * typed word narrows them too.
 */
internal fun mentionRows(mentions: ComposerMentions, query: String): List<MentionRow> {
    val q = query.lowercase()
    val tasks = mentions.tasks.orEmpty().filter { q.isEmpty() || it.title.lowercase().contains(q) }.map { MentionRow.Task(it) }
    val files = mentions.files.map { relativeMentionPath(it, mentions.cwd) to it }
        .filter { (relative, _) -> q.isEmpty() || relative.lowercase().contains(q) }
        .map { (relative, path) -> MentionRow.File(path, "@$relative ") }
    return tasks + files + otherSessions(mentions.sessions, mentions.except, query).map { MentionRow.Quote(it) }
}

/** The quote for a picked session (iOS `PromptItems.quote`): its title, agent, folder and last words, as context. */
internal fun sessionQuote(session: Session, resources: Resources): ChatLinkContext {
    val lines = buildList {
        add(resources.getString(R.string.chat_mention_quote_session, session.displayTitle, session.agentLabel))
        add(resources.getString(R.string.chat_mention_quote_cwd, session.cwd))
        session.lastText?.trim()?.takeIf(String::isNotEmpty)?.let { add(resources.getString(R.string.chat_mention_quote_last, it.take(600))) }
    }
    return ChatLinkContext("context", session.displayTitle, "persona://session/${session.machineId}/${session.sessionId}", lines.joinToString("\n"))
}
