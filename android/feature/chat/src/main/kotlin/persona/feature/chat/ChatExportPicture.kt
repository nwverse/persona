package persona.feature.chat

import android.graphics.Bitmap
import android.graphics.Canvas
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.ComposeView
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.Density
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import persona.core.contracts.api.ReplyImageSource
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.feature.chat.media.MediaShare
import java.time.Instant
import java.util.Locale
import kotlin.math.sqrt

/** Phone-width PNGs, one bitmap at a time. The limit is in output pixels, independent of the device density. */
internal const val MAX_EXPORT_HEIGHT = 16_000
private const val ExportDensity = 2f
private const val ExportWidth = 780 // 390 dp at 2x, including margins

internal data class ExportSlice(val block: Int, val from: Int, val to: Int, val gap: Int)

internal fun exportPagePlan(heights: List<Int>, gap: Int, room: Int): List<List<ExportSlice>> {
    require(room > 0 && gap >= 0)
    val pages = mutableListOf<List<ExportSlice>>()
    var page = mutableListOf<ExportSlice>()
    var used = 0
    fun finish() {
        if (page.isNotEmpty()) pages += page
        page = mutableListOf()
        used = 0
    }
    heights.forEachIndexed { index, height ->
        if (height <= 0) return@forEachIndexed
        if (page.isNotEmpty() && height > room - used - gap) finish()
        var from = 0
        while (from < height) {
            val before = if (page.isEmpty()) 0 else gap
            val length = minOf(height - from, room - used - before)
            page += ExportSlice(index, from, from + length, before)
            used += before + length
            from += length
            if (used == room) finish()
        }
    }
    finish()
    return pages
}

/** Compose bounds are finite even when a single short message consists of thousands of newlines. */
internal fun pictureParts(text: String, markdown: Boolean): List<String> {
    val groupSize = 512
    if (text.count { it == '\n' } < groupSize) return listOf(text)
    val groups = text.lines().chunked(groupSize)
    if (!markdown) return groups.map { it.joinToString("\n") }
    var fence: Pair<Char, Int>? = null
    var opening = ""
    return groups.mapIndexed { index, lines ->
        buildString {
            if (fence != null) appendLine(opening)
            for ((i, line) in lines.withIndex()) {
                if (i > 0) append('\n')
                append(line)
                val trimmed = line.trimStart()
                val current = fence
                if (current == null) {
                    fence = ShareCardBlocks.fenceOf(trimmed)
                    if (fence != null) opening = line
                } else if (ShareCardBlocks.closes(current, trimmed)) fence = null
            }
            if (index < groups.lastIndex) fence?.let { (char, count) -> append('\n').append(char.toString().repeat(count)) }
        }
    }
}

private data class PicturePart(val message: PickedMessage, val text: String, val index: Int) {
    val id = "picked/${message.id}/$index"
}

@Composable
private fun PictureMessage(part: PicturePart, photos: Map<String, ImageBitmap?>, gallery: ReplyGallery?) {
    val message = part.message
    Column(Modifier.fillMaxWidth(), horizontalAlignment = if (message.user) Alignment.End else Alignment.Start) {
        if (message.user) {
            if (part.index == 0) message.photos.forEach { path ->
                val image = photos[path]
                if (image == null) Text(stringResource(R.string.chat_pick_photo_missing), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                else Image(image, null, Modifier.fillMaxWidth().heightIn(max = ContentTokens.mediaPreviewMaxHeight))
            }
            if (part.text.isNotEmpty()) UserBubbleFrame {
                Text(messageLinkAnnotations(part.text), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
            }
        } else {
            if (part.index == 0) Text(message.author, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            if (part.text.isNotEmpty()) CompositionLocalProvider(LocalReplyGallery provides gallery) {
                ChatMarkdown(gallery?.document?.markdown ?: part.text, part.id, selectable = false)
            }
        }
    }
}

@Composable
private fun PictureFooter(page: Int, count: Int) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        JIcon(PersonaIcons.ai, null, size = Tokens.Size.markXS, tint = PersonaTheme.palette.ink3)
        Text(stringResource(R.string.chat_share_card_brand), Modifier.weight(1f), style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3)
        if (count > 1) Text("$page/$count", style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3)
    }
}


internal fun exportSubtitle(messages: List<PickedMessage>, agent: String, locale: Locale, now: Instant = Instant.now()): String {
    val times = messages.mapNotNull { it.time }
    val first = LocalizedFormats.format(times.minOrNull() ?: now, LocalizedFormats.DATE, locale)
    val last = LocalizedFormats.format(times.maxOrNull() ?: now, LocalizedFormats.DATE, locale)
    return listOf(if (first == last) first else "$first – $last", agent).filter { it.isNotBlank() }.joinToString(" · ")
}

/** Export never grants a direct image load. As in chat, only the paired Hub can fetch an agent's own pictures automatically. */
internal suspend fun exportGallery(doc: ReplyImageDocument, ownReply: Boolean, source: ReplyImageSource,
    pictures: MutableMap<String, Bitmap?>, maxSide: Int): ReplyGallery {
    val through = ReplyImageSource.FEATURE in source.features.value
    if (ownReply && through) for (picture in doc.visiblePictures()) {
        if (picture.url in pictures) continue
        pictures[picture.url] = try {
            val size = ReplyImageSource.sizeFor(maxSide.toFloat())
            val bytes = source.load(picture.url, size, allowDirect = false)
            withContext(Dispatchers.Default) { decodePicture(bytes, maxSide, software = true) }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { null }
    }
    return ReplyGallery(doc, source, through, ownReply, mutableMapOf(), { _, _ -> }, exported = pictures)
}

/** Each measured block is the same Compose Markdown/bubble used by the chat, with the window's theme and type scale.
 * Draw just the part of a block that fits on each page, including messages that alone exceed a page. */
internal suspend fun renderPickedPages(owner: View, parent: CompositionContext, style: AppStyle, dark: Boolean,
    messages: List<PickedMessage>, title: String, heading: Boolean, photos: AttachedPhotos?,
    agent: String = "", replyImages: ReplyImageSource? = null, maxHeight: Int = MAX_EXPORT_HEIGHT): List<ExportPage> = withContext(Dispatchers.Main.immediate) {
    val root = owner.rootView.findViewById<ViewGroup>(android.R.id.content)
        ?: error("No attached chat window")
    val context = owner.context
    val cache = ChatMarkdownCache(scope = CoroutineScope(coroutineContext + Dispatchers.Default))
    val parts = messages.flatMap { message -> pictureParts(message.text, markdown = !message.user).mapIndexed { i, text -> PicturePart(message, text, i) } }
    val pictures = mutableMapOf<String, ImageBitmap?>()
    cache.keep(parts.filterNot { it.message.user }.map { it.id })
    val documents = withContext(Dispatchers.Default) {
        parts.filter { !it.message.user && replyImages != null && "![" in it.text }
            .mapNotNull { part -> ReplyImageDocument.parse(part.text)?.let { part.id to it } }.toMap()
    }
    val photoPaths = messages.flatMap { it.photos }.distinct()
    // Keep decoded source pictures within 16 MiB besides the current output page. A single photo still gets the full column.
    val remoteCount = documents.values.flatMap { it.visiblePictures() }.distinctBy { it.url }.size
    val maxSide = sqrt((4 * 1024 * 1024).toDouble() / (remoteCount + photoPaths.size).coerceAtLeast(1)).toInt().coerceIn(1, ExportWidth)
    val replyPictures = mutableMapOf<String, Bitmap?>()
    val galleries = parts.associate { part ->
        val gallery = documents[part.id]?.let { exportGallery(it, part.message.ownReply, requireNotNull(replyImages), replyPictures, maxSide) }
        if (!part.message.user) cache.parseRenderState(part.id, PathLinks.markdown(gallery?.document?.markdown ?: part.text, emptySet()))
        part.id to gallery
    }
    for (path in photoPaths) {
        pictures[path] = try { photos?.thumbnail(path, minOf(360, maxSide), forExport = true) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { null }
    }
    val density = Density(ExportDensity, context.resources.configuration.fontScale)
    val margin = with(density) { Tokens.Space.xl.roundToPx() }
    val gap = with(density) { Tokens.Space.l.roundToPx() }
    val contentWidth = ExportWidth - 2 * margin
    // The window can traverse again while a page is encoded. Its layout must not resize the measured blocks.
    val host = object : FrameLayout(context) {
        override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) = setMeasuredDimension(1, 1)
        override fun onLayout(changed: Boolean, left: Int, top: Int, right: Int, bottom: Int) = Unit
    }.apply { alpha = 0f; importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS }
    root.addView(host, 1, 1)
    val views = mutableListOf<ComposeView>()
    val output = mutableListOf<ExportPage>()
    var completed = false
    var background = 0
    fun block(content: @Composable () -> Unit): ComposeView = ComposeView(context).apply {
        setParentCompositionContext(parent)
        setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                background = PersonaTheme.palette.background.toArgb()
                CompositionLocalProvider(LocalDensity provides density, LocalChatMarkdownCache provides cache,
                    LocalChatRowActions provides null, LocalInPicture provides true) { content() }
            }
        }
        host.addView(this)
        views += this
        measure(View.MeasureSpec.makeMeasureSpec(contentWidth, View.MeasureSpec.EXACTLY), View.MeasureSpec.UNSPECIFIED)
        layout(0, 0, contentWidth, measuredHeight)
    }
    try {
        val blocks = buildList {
            if (heading) add(listOf(block {
                Column {
                    if (title.isNotBlank()) Text(title, style = PersonaTheme.type.title, color = PersonaTheme.palette.ink)
                    Text(exportSubtitle(messages, agent, currentLocale), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                }
            }))
            // Measure bounded fragments for Android, but paginate the entire message as one block.
            for (message in parts.groupBy { it.message.id }.values) {
                add(message.map { part -> block { PictureMessage(part, pictures, galleries[part.id]) } })
            }
        }
        val footerSample = block { PictureFooter(1, 2) }
        val plan = exportPagePlan(blocks.map { group -> group.sumOf { it.height } }, gap, maxHeight - 2 * margin - gap - footerSample.height)
        for ((index, slices) in plan.withIndex()) {
            val footer = block { PictureFooter(index + 1, plan.size) }
            val height = 2 * margin + slices.sumOf { it.gap + it.to - it.from } + gap + footer.height
            val bitmap = Bitmap.createBitmap(ExportWidth, height, Bitmap.Config.ARGB_8888)
            try {
                bitmap.eraseColor(background)
                val canvas = Canvas(bitmap)
                var y = margin
                for (slice in slices) {
                    y += slice.gap
                    canvas.save()
                    canvas.clipRect(margin, y, margin + contentWidth, y + slice.to - slice.from)
                    canvas.translate(margin.toFloat(), (y - slice.from).toFloat())
                    var offset = 0
                    for (view in blocks[slice.block]) {
                        if (offset >= slice.to) break
                        if (offset + view.height > slice.from) view.draw(canvas)
                        canvas.translate(0f, view.height.toFloat())
                        offset += view.height
                    }
                    canvas.restore()
                    y += slice.to - slice.from
                }
                canvas.save()
                canvas.translate(margin.toFloat(), (y + gap).toFloat())
                footer.draw(canvas)
                canvas.restore()
                val file = withContext(Dispatchers.IO) { MediaShare.writePng(context, bitmap, index + 1) }
                output += ExportPage(file, bitmap.width, bitmap.height)
            } finally { bitmap.recycle() }
        }
        completed = true
        output
    } finally {
        views.forEach { it.disposeComposition() }
        root.removeView(host)
        if (!completed) output.forEach { it.file.delete() }
    }
}
