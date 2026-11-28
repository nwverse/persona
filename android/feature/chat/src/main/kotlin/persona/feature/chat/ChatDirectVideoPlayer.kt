package persona.feature.chat

import android.media.MediaPlayer
import android.net.Uri
import android.view.Gravity
import android.view.ViewGroup
import android.webkit.CookieManager
import android.widget.FrameLayout
import android.widget.VideoView
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JIconButton
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons
import java.net.URI
import java.net.URLDecoder
import java.nio.charset.StandardCharsets

/** Direct video file extensions rendered natively in [ChatLinkHost] instead of Chromium MediaDocument. */
internal val directVideoExtensions = setOf("mp4", "m4v", "webm", "mov", "mkv", "3gp", "3gpp")

/** Returns true when [url] is an `http(s)` link whose path ends with a common direct video extension. */
internal fun isDirectVideoUrl(url: String): Boolean {
    val safe = ChatLinks.webUrl(url) ?: return false
    return extractUrlExtension(safe) in directVideoExtensions
}

/** Extracts the lowercase file extension from the path component of [url], ignoring query and fragment. */
internal fun extractUrlExtension(url: String): String {
    val path = runCatching { URI(url.trim()).path }.getOrNull()?.lowercase() ?: return ""
    val segment = path.substringAfterLast('/', "")
    val ext = segment.substringAfterLast('.', "")
    return if (ext.isNotEmpty() && ext != segment) ext else ""
}

/** Extracts a readable video file name from [url] for the sheet title, or null if none is present. */
internal fun directVideoTitle(url: String): String? {
    val rawSegment = runCatching { URI(url.trim()).rawPath }.getOrNull()
        ?.substringAfterLast('/', "")
        ?.takeIf { it.isNotBlank() }
        ?: return null
    val decoded = runCatching { URLDecoder.decode(rawSegment, StandardCharsets.UTF_8.name()) }
        .getOrDefault(rawSegment)
        .trim()
    return decoded.takeIf { it.isNotBlank() }
}

/**
 * Returns true when an HTTP response delivers inline video content rather than an HTML page or an
 * explicit `Content-Disposition: attachment` download.
 */
internal fun isInlineVideoResponse(
    statusCode: Int,
    contentType: String?,
    contentDisposition: String?,
): Boolean {
    if (statusCode !in 200..299) return false
    val baseMime = contentType?.substringBefore(';')?.trim()?.lowercase().orEmpty()
    if (!baseMime.startsWith("video/")) return false
    val disposition = contentDisposition?.substringBefore(';')?.trim()?.lowercase().orEmpty()
    if (disposition == "attachment") return false
    return true
}

/** Formats a media timestamp in milliseconds as `m:ss` or `h:mm:ss`. */
internal fun formatVideoTime(millis: Int): String {
    val totalSeconds = (millis.coerceAtLeast(0)) / 1000
    val hours = totalSeconds / 3600
    val minutes = (totalSeconds % 3600) / 60
    val seconds = totalSeconds % 60
    return if (hours > 0) "%d:%02d:%02d".format(hours, minutes, seconds)
    else "%d:%02d".format(minutes, seconds)
}

/**
 * Lightweight probe for extensionless or redirected `http(s)` links that respond with inline `video/` MIME types.
 * Returns the terminal playable URL when the response is inline video, or null otherwise.
 */
internal fun interface ChatDirectVideoProber {
    suspend fun probe(url: String): String?

    companion object {
        val Platform = ChatDirectVideoProber { url ->
            withContext(Dispatchers.IO) { probeDirectVideoUrl(url) }
        }
    }
}

internal val LocalChatDirectVideoProber = staticCompositionLocalOf<ChatDirectVideoProber> {
    ChatDirectVideoProber.Platform
}

internal fun probeDirectVideoUrl(
    initialUrl: String,
    prober: WebDownloadProber = WebDownloadProber.Platform,
    cookieForUrl: (String) -> String? = ::directVideoCookieForUrl,
): String? {
    val startUrl = ChatLinks.webUrl(initialUrl) ?: return null
    val startOrigin = runCatching { URI(startUrl) }.getOrNull()
    var currentUrl = startUrl
    for (hop in 0..MAX_DIRECT_VIDEO_REDIRECTS) {
        val sameOrigin = isSameDirectVideoOrigin(startOrigin, runCatching { URI(currentUrl) }.getOrNull())
        val cookie = if (sameOrigin) runCatching { cookieForUrl(currentUrl) }.getOrNull()?.takeIf { it.isNotBlank() } else null
        val response = try {
            val head = prober.probe(
                WebDownloadProbeRequest(
                    url = currentUrl,
                    method = "HEAD",
                    cookie = cookie,
                    userAgent = null,
                    referer = null,
                ),
            )
            if (head.statusCode in DIRECT_VIDEO_HEAD_FALLBACK_CODES) {
                prober.probe(
                    WebDownloadProbeRequest(
                        url = currentUrl,
                        method = "GET",
                        cookie = cookie,
                        userAgent = null,
                        referer = null,
                    ),
                )
            } else {
                head
            }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Throwable) {
            return null
        }

        if (response.statusCode in DIRECT_VIDEO_REDIRECT_CODES) {
            val location = response.location ?: return null
            if (hop == MAX_DIRECT_VIDEO_REDIRECTS) return null
            val resolved = runCatching { URI(currentUrl).resolve(location).toString() }.getOrNull() ?: return null
            currentUrl = ChatLinks.webUrl(resolved) ?: return null
            continue
        }

        return if (isInlineVideoResponse(response.statusCode, response.contentType, response.contentDisposition)) {
            currentUrl
        } else {
            null
        }
    }
    return null
}

private fun isSameDirectVideoOrigin(first: URI?, second: URI?): Boolean {
    if (first == null || second == null) return false
    val firstScheme = first.scheme?.lowercase() ?: return false
    val secondScheme = second.scheme?.lowercase() ?: return false
    val firstHost = first.host?.lowercase() ?: return false
    val secondHost = second.host?.lowercase() ?: return false
    return firstScheme == secondScheme && firstHost == secondHost && effectivePort(first) == effectivePort(second)
}

private fun effectivePort(uri: URI): Int = when {
    uri.port > 0 -> uri.port
    uri.scheme.equals("https", ignoreCase = true) -> 443
    uri.scheme.equals("http", ignoreCase = true) -> 80
    else -> -1
}

private fun directVideoCookieForUrl(url: String): String? =
    runCatching { CookieManager.getInstance().getCookie(url) }.getOrNull()

@Stable
internal class ChatDirectVideoState {
    var isPrepared by mutableStateOf(false)
    var isPlaying by mutableStateOf(false)
    var isBuffering by mutableStateOf(true)
    var isMuted by mutableStateOf(false)
    var hasError by mutableStateOf(false)
    var durationMs by mutableIntStateOf(0)
    var positionMs by mutableIntStateOf(0)
    var resumePositionMs by mutableIntStateOf(0)
    var shouldAutoPlay by mutableStateOf(true)
    var controlsVisible by mutableStateOf(true)
    var retryNonce by mutableIntStateOf(0)

    fun resetForReload() {
        isPrepared = false
        isPlaying = false
        isBuffering = true
        hasError = false
        durationMs = 0
        positionMs = 0
        resumePositionMs = 0
        shouldAutoPlay = true
        controlsVisible = true
        retryNonce++
    }
}

/**
 * Native video player rendered inside [ChatLinkHost] (inline in the link sheet or fullscreen) for direct
 * video URLs so they play with hardware decoding and native transport controls instead of Chromium's
 * black, control-less MediaDocument.
 */
@Composable
internal fun ChatDirectVideoPlayer(
    url: String,
    state: ChatDirectVideoState,
    fullscreen: Boolean,
    onFullscreenChange: (Boolean) -> Unit,
    onOpenInBrowser: () -> Unit,
    onDownload: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    val palette = PersonaTheme.palette
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var videoViewRef by remember { mutableStateOf<VideoView?>(null) }
    var mediaPlayerRef by remember { mutableStateOf<MediaPlayer?>(null) }
    var isScrubbing by remember { mutableStateOf(false) }
    var scrubPositionMs by remember { mutableIntStateOf(0) }
    var controlsInteractionTick by remember { mutableIntStateOf(0) }

    fun applyVolume(mp: MediaPlayer?, muted: Boolean) {
        val vol = if (muted) 0f else 1f
        runCatching { mp?.setVolume(vol, vol) }
    }

    fun togglePlayPause() {
        controlsInteractionTick++
        val vv = videoViewRef ?: return
        if (!state.isPrepared) return
        if (state.isPlaying) {
            vv.pause()
            state.resumePositionMs = vv.currentPosition.coerceAtLeast(0)
            state.positionMs = state.resumePositionMs
            state.isPlaying = false
            state.shouldAutoPlay = false
            state.controlsVisible = true
        } else {
            if (state.durationMs > 0 && state.positionMs >= state.durationMs - 250) {
                vv.seekTo(0)
                state.positionMs = 0
                state.resumePositionMs = 0
            }
            vv.start()
            state.isPlaying = true
            state.shouldAutoPlay = true
        }
    }

    fun toggleMute() {
        controlsInteractionTick++
        state.isMuted = !state.isMuted
        applyVolume(mediaPlayerRef, state.isMuted)
    }

    // Poll playback position smoothly while playing.
    LaunchedEffect(state.isPlaying, state.isPrepared, isScrubbing, videoViewRef) {
        while (state.isPlaying && state.isPrepared && !isScrubbing) {
            val vv = videoViewRef
            if (vv != null) {
                val pos = runCatching { vv.currentPosition }.getOrDefault(state.positionMs).coerceAtLeast(0)
                state.positionMs = pos
                state.resumePositionMs = pos
            }
            delay(250)
        }
    }

    // Auto-hide controls after a few seconds of uninterrupted playback.
    LaunchedEffect(state.controlsVisible, state.isPlaying, isScrubbing, state.hasError, controlsInteractionTick) {
        if (state.controlsVisible && state.isPlaying && !isScrubbing && !state.hasError) {
            delay(4_000)
            state.controlsVisible = false
        }
    }

    // Pause cleanly when the host lifecycle stops.
    DisposableEffect(lifecycle, videoViewRef) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) {
                videoViewRef?.let { vv ->
                    if (state.isPlaying) {
                        state.resumePositionMs = runCatching { vv.currentPosition }.getOrDefault(state.positionMs).coerceAtLeast(0)
                        runCatching { vv.pause() }
                        state.isPlaying = false
                    }
                }
            }
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(palette.mediaBackdrop)
            .testTag("chat_direct_video_player")
            .pointerInput(Unit) {
                detectTapGestures {
                    state.controlsVisible = !state.controlsVisible
                    controlsInteractionTick++
                }
            },
    ) {
        if (!state.hasError) {
            key(url, state.retryNonce, fullscreen) {
                AndroidView(
                    factory = { ctx ->
                        val container = FrameLayout(ctx).apply {
                            layoutParams = ViewGroup.LayoutParams(
                                ViewGroup.LayoutParams.MATCH_PARENT,
                                ViewGroup.LayoutParams.MATCH_PARENT,
                            )
                        }
                        val videoView = VideoView(ctx).apply {
                            layoutParams = FrameLayout.LayoutParams(
                                ViewGroup.LayoutParams.WRAP_CONTENT,
                                ViewGroup.LayoutParams.WRAP_CONTENT,
                                Gravity.CENTER,
                            )
                            setOnPreparedListener { mp ->
                                mediaPlayerRef = mp
                                applyVolume(mp, state.isMuted)
                                state.durationMs = mp.duration.coerceAtLeast(0)
                                state.isPrepared = true
                                state.isBuffering = false
                                state.hasError = false
                                mp.setOnInfoListener { _, what, _ ->
                                    when (what) {
                                        MediaPlayer.MEDIA_INFO_BUFFERING_START -> state.isBuffering = true
                                        MediaPlayer.MEDIA_INFO_BUFFERING_END,
                                        MediaPlayer.MEDIA_INFO_VIDEO_RENDERING_START,
                                        -> state.isBuffering = false
                                    }
                                    false
                                }
                                if (state.resumePositionMs > 0) {
                                    seekTo(state.resumePositionMs)
                                    state.positionMs = state.resumePositionMs
                                }
                                if (state.shouldAutoPlay) {
                                    start()
                                    state.isPlaying = true
                                } else {
                                    // Render the initial frame when paused.
                                    seekTo(state.resumePositionMs.coerceAtLeast(1))
                                    state.isPlaying = false
                                }
                            }
                            setOnCompletionListener {
                                state.isPlaying = false
                                state.shouldAutoPlay = false
                                state.positionMs = state.durationMs
                                state.resumePositionMs = state.durationMs
                                state.controlsVisible = true
                            }
                            setOnErrorListener { _, _, _ ->
                                state.hasError = true
                                state.isBuffering = false
                                state.isPlaying = false
                                state.isPrepared = false
                                state.controlsVisible = true
                                true
                            }
                            val headers = directVideoCookieForUrl(url)
                                ?.takeIf { it.isNotBlank() }
                                ?.let { mapOf("Cookie" to it) }
                                ?: emptyMap()
                            setVideoURI(Uri.parse(url), headers)
                        }
                        videoViewRef = videoView
                        container.addView(videoView)
                        container
                    },
                    modifier = Modifier.fillMaxSize(),
                    onRelease = { container ->
                        val vv = container.getChildAt(0) as? VideoView
                        if (vv != null) {
                            if (state.isPrepared) {
                                state.resumePositionMs = runCatching { vv.currentPosition }.getOrDefault(state.positionMs).coerceAtLeast(0)
                            }
                            runCatching { vv.stopPlayback() }
                        }
                        if (videoViewRef === vv) videoViewRef = null
                        mediaPlayerRef = null
                    },
                )
            }
        }

        if (state.isBuffering && !state.hasError) {
            CircularProgressIndicator(
                modifier = Modifier
                    .align(Alignment.Center)
                    .size(Tokens.Space.Standard.xl)
                    .testTag("chat_direct_video_loading"),
                color = palette.onMediaBackdrop,
            )
        }

        if (state.hasError) {
            Column(
                modifier = Modifier
                    .align(Alignment.Center)
                    .padding(Tokens.Space.l)
                    .testTag("chat_direct_video_error"),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.m),
            ) {
                Text(
                    text = stringResource(R.string.chat_link_video_error),
                    style = PersonaTheme.type.body,
                    color = palette.onMediaBackdrop,
                    textAlign = TextAlign.Center,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    JButton(
                        text = stringResource(R.string.chat_link_retry),
                        onClick = { state.resetForReload() },
                        kind = JButtonKind.Primary,
                        size = JButtonSize.Small,
                    )
                    JButton(
                        text = stringResource(R.string.chat_link_open_browser),
                        onClick = onOpenInBrowser,
                        kind = JButtonKind.Secondary,
                        size = JButtonSize.Small,
                    )
                }
            }
        } else if (state.controlsVisible || !state.isPlaying) {
            val shownPosition = if (isScrubbing) scrubPositionMs else state.positionMs
            val sliderMax = state.durationMs.coerceAtLeast(1).toFloat()
            val sliderValue = shownPosition.coerceIn(0, state.durationMs.coerceAtLeast(1)).toFloat()
            Column(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .background(palette.mediaLabelFill)
                    .navigationBarsPadding()
                    .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.xs)
                    .testTag("chat_direct_video_controls"),
            ) {
                Slider(
                    value = sliderValue,
                    onValueChange = { nextValue ->
                        isScrubbing = true
                        controlsInteractionTick++
                        scrubPositionMs = nextValue.toInt()
                    },
                    onValueChangeFinished = {
                        val targetMs = scrubPositionMs
                        videoViewRef?.let { vv ->
                            if (state.isPrepared) {
                                runCatching { vv.seekTo(targetMs) }
                            }
                        }
                        state.positionMs = targetMs
                        state.resumePositionMs = targetMs
                        isScrubbing = false
                    },
                    valueRange = 0f..sliderMax,
                    enabled = state.isPrepared && state.durationMs > 0,
                    colors = SliderDefaults.colors(
                        thumbColor = palette.onMediaBackdrop,
                        activeTrackColor = palette.accent,
                        inactiveTrackColor = palette.onMediaBackdrop.copy(alpha = 0.3f),
                    ),
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("chat_direct_video_slider"),
                )
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    JIconButton(
                        icon = if (state.isPlaying) PersonaIcons.pause else PersonaIcons.playMedia,
                        contentDescription = stringResource(
                            if (state.isPlaying) R.string.chat_link_video_pause else R.string.chat_link_video_play,
                        ),
                        onClick = ::togglePlayPause,
                        tint = palette.onMediaBackdrop,
                    )
                    Text(
                        text = "${formatVideoTime(shownPosition)} / ${formatVideoTime(state.durationMs)}",
                        style = PersonaTheme.type.micro,
                        color = palette.onMediaBackdrop,
                        modifier = Modifier
                            .padding(start = Tokens.Space.xs)
                            .testTag("chat_direct_video_time"),
                    )
                    Spacer(Modifier.weight(1f))
                    JIconButton(
                        icon = PersonaIcons.voiceSpeaking,
                        contentDescription = stringResource(
                            if (state.isMuted) R.string.chat_link_video_unmute else R.string.chat_link_video_mute,
                        ),
                        onClick = ::toggleMute,
                        tint = palette.onMediaBackdrop,
                        modifier = Modifier.alpha(if (state.isMuted) 0.4f else 1f),
                    )
                    if (onDownload != null) {
                        JIconButton(
                            icon = PersonaIcons.down,
                            contentDescription = stringResource(R.string.chat_link_download),
                            onClick = {
                                controlsInteractionTick++
                                onDownload()
                            },
                            tint = palette.onMediaBackdrop,
                        )
                    }
                    JIconButton(
                        icon = if (fullscreen) PersonaIcons.collapseMessage else PersonaIcons.expandMessage,
                        contentDescription = stringResource(
                            if (fullscreen) R.string.chat_link_video_exit_fullscreen else R.string.chat_link_video_fullscreen,
                        ),
                        onClick = {
                            controlsInteractionTick++
                            videoViewRef?.let { vv ->
                                if (state.isPrepared) {
                                    state.resumePositionMs = runCatching { vv.currentPosition }.getOrDefault(state.positionMs).coerceAtLeast(0)
                                    state.shouldAutoPlay = state.isPlaying
                                }
                            }
                            onFullscreenChange(!fullscreen)
                        },
                        tint = palette.onMediaBackdrop,
                    )
                }
            }
        }
    }
}

private const val MAX_DIRECT_VIDEO_REDIRECTS = 5
private val DIRECT_VIDEO_REDIRECT_CODES = setOf(301, 302, 303, 307, 308)
private val DIRECT_VIDEO_HEAD_FALLBACK_CODES = setOf(403, 405, 501)
