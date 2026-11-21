package persona.feature.chat

import android.content.res.Resources
import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.layout.ContentScale
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.feature.chat.media.MediaShare
import java.io.File
import java.time.Instant

internal data class PickedMessage(val id: String, val user: Boolean, val author: String, val text: String,
    val time: Instant?, val photos: List<String>, val ownReply: Boolean = false)

internal suspend fun pickedMessages(drafts: List<ForwardDraft>, resources: Resources, read: suspend (String) -> String) = drafts.map { draft ->
    val source = draft.source
    val user = source.role == ForwardMessage.Role.User
    PickedMessage(source.item, user, if (user) resources.getString(R.string.chat_forward_me) else source.author,
        ForwardMessage.body(draft.cut?.let { read(it) } ?: draft.raw, source.machine, user, resources),
        source.time, draft.photos, source.role == ForwardMessage.Role.Agent || source.role == ForwardMessage.Role.Persona)
}

internal fun copiedMessages(messages: List<PickedMessage>, resources: Resources) = messages.joinToString("\n\n") {
    resources.getString(R.string.chat_pick_author, it.author) + "\n" + it.text
}

@Composable
internal fun PickedMessageExport(pick: ForwardPick, drafts: List<ForwardDraft>, key: SessionKey,
    repository: ChatRepository, photos: AttachedPhotos?, agent: String, notice: (String) -> Unit) {
    val resources = LocalResources.current
    val copy = rememberCopy()
    val action = pick.action
    var attempt by remember(drafts) { mutableIntStateOf(0) }
    var messages by remember(drafts) { mutableStateOf<List<PickedMessage>?>(null) }
    var failed by remember(drafts) { mutableStateOf(false) }
    LaunchedEffect(drafts, action, attempt) {
        failed = false
        try {
            val whole = pickedMessages(drafts, resources) { repository.fullText(key, it) }
            if (action == PickAction.Copy) {
                if (copy(copiedMessages(whole, resources), null)) pick.end() else pick.dismiss()
            } else messages = whole
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            if (action == PickAction.Copy) { notice(resources.getString(R.string.chat_pick_failed)); pick.dismiss() } else failed = true
        }
    }
    if (action == PickAction.Export) {
        ChatExportPreview(messages, drafts.first().source.chat, agent, photos, failed, retry = { attempt++ }, dismiss = pick::dismiss)
    }
}

internal data class ExportPage(val file: File, val width: Int, val height: Int)

@Composable
private fun ChatExportPreview(messages: List<PickedMessage>?, title: String, agent: String, photos: AttachedPhotos?, loadFailed: Boolean,
    retry: () -> Unit, dismiss: () -> Unit) {
    val context = LocalContext.current
    val resources = LocalResources.current
    val replyImages = LocalChatRowActions.current?.replyImages
    val owner = LocalView.current
    val parent = rememberCompositionContext()
    val style = PersonaTheme.style
    val dark = PersonaTheme.darkTheme
    val scope = rememberCoroutineScope()
    var heading by remember { mutableStateOf(true) }
    var attempt by remember { mutableIntStateOf(0) }
    var pages by remember { mutableStateOf<List<ExportPage>>(emptyList()) }
    var failure by remember { mutableStateOf(false) }
    val toast = rememberToastState()
    LaunchedEffect(messages, heading, dark, style, attempt, replyImages) {
        pages = emptyList()
        failure = false
        if (messages != null) try {
            pages = renderPickedPages(owner, parent, style, dark, messages, title, heading, photos, agent, replyImages)
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { failure = true }
    }
    JSheet(dismiss, title = stringResource(R.string.chat_pick_export)) {
        Column(Modifier.fillMaxWidth().testTag("export-preview"), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JSwitchRow(stringResource(R.string.chat_pick_heading), heading, { heading = it })
            when {
                loadFailed || failure -> {
                    Text(stringResource(R.string.chat_pick_failed), color = PersonaTheme.palette.danger.text, style = PersonaTheme.type.callout)
                    JTextButton(stringResource(R.string.chat_attach_retry), { if (loadFailed) retry() else attempt++ })
                }
                pages.isEmpty() -> Text(stringResource(R.string.chat_pick_working), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
                else -> LazyColumn(Modifier.fillMaxWidth().weight(1f, fill = false),
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.l)) {
                    items(pages.size) { index ->
                        val page = pages[index]
                        val label = stringResource(R.string.chat_pick_page, index + 1, pages.size)
                        if (pages.size > 1) Text(label, color = PersonaTheme.palette.ink2, style = PersonaTheme.type.caption)
                        val bitmap by produceState<ImageBitmap?>(null, page.file) {
                            value = withContext(Dispatchers.IO) {
                                val sample = if (page.height > page.width * 4) 2 else 1
                                BitmapFactory.decodeFile(page.file.path, BitmapFactory.Options().apply { inSampleSize = sample })?.asImageBitmap()
                            }
                        }
                        Box(Modifier.fillMaxWidth().aspectRatio(page.width.toFloat() / page.height).testTag("export-page-${index + 1}")) {
                            bitmap?.let { Image(it, label, Modifier.fillMaxSize(), contentScale = ContentScale.FillBounds) }
                        }
                    }
                }
            }
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                JTextButton(stringResource(R.string.chat_delegated_close), dismiss)
                JButton(stringResource(R.string.chat_viewer_share), {
                    scope.launch {
                        if (!MediaShare.sharePngs(context, pages.map { it.file })) toast.show(FeedbackNotice(resources.getString(R.string.chat_menu_no_share), style = FeedbackStyle.Error))
                    }
                }, Modifier.testTag("export-share"), enabled = pages.isNotEmpty(), icon = PersonaIcons.share)
            }
            JToastHost(toast)
        }
    }
}
