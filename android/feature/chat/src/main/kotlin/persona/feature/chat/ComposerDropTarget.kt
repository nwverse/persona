package persona.feature.chat

import android.app.Activity
import android.content.ClipData
import android.content.ClipDescription
import android.content.Context
import android.content.ContextWrapper
import android.net.Uri
import android.view.DragAndDropPermissions
import android.view.DragEvent
import androidx.compose.ui.draganddrop.DragAndDropEvent
import androidx.compose.ui.draganddrop.DragAndDropTarget
import androidx.compose.ui.draganddrop.toAndroidDragEvent

/** A drag target that only forwards readable-file candidates; text and unrelated payloads stay with the text field. */
internal class ComposerDropTarget(
    private val accepts: () -> Boolean,
    private val hovering: (Boolean) -> Unit,
    private val drop: (DragEvent) -> Boolean,
) : DragAndDropTarget {
    override fun onEntered(event: DragAndDropEvent) {
        if (accepts()) hovering(true)
    }

    override fun onExited(event: DragAndDropEvent) {
        hovering(false)
    }

    override fun onEnded(event: DragAndDropEvent) {
        hovering(false)
    }

    override fun onDrop(event: DragAndDropEvent): Boolean {
        hovering(false)
        return accepts() && drop(event.toAndroidDragEvent())
    }
}

/** Conservative started-event filtering: plain text / HTML / URI lists are not file drops. */
internal fun composerDropAccepts(description: ClipDescription?): Boolean = composerDropAcceptsMimeTypes(
    description?.let { clip -> (0 until clip.mimeTypeCount).map(clip::getMimeType) },
)

internal fun composerDropAcceptsMimeTypes(types: Iterable<String>?): Boolean {
    val present = types?.toList().orEmpty()
    return present.isNotEmpty() && present.any { type ->
        type != ClipDescription.MIMETYPE_TEXT_PLAIN && type != ClipDescription.MIMETYPE_TEXT_HTML &&
            type != ClipDescription.MIMETYPE_TEXT_URILIST && !type.startsWith("text/")
    }
}

/** Only URI-bearing file/content items are imported; text, intents and other payload kinds are ignored. */
internal fun composerDropUris(data: ClipData?): List<Uri> = buildList {
    data ?: return@buildList
    for (index in 0 until data.itemCount) {
        val uri = data.getItemAt(index).uri ?: continue
        if (uri.scheme == "content" || uri.scheme == "file") if (none { it == uri }) add(uri)
    }
}

/** Keep the transient grant while the drop is copied; release it only after every item has finished importing. */
internal fun Context.requestComposerDropPermissions(event: DragEvent): DragAndDropPermissions? = runCatching {
    activityForDrop()?.requestDragAndDropPermissions(event)
}.getOrNull()

private tailrec fun Context.activityForDrop(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.activityForDrop()
    else -> null
}
