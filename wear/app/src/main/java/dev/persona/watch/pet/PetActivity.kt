package dev.persona.watch.pet

import android.app.Activity
import android.graphics.Bitmap
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.os.SystemClock
import android.provider.Settings
import android.view.Gravity
import android.view.HapticFeedbackConstants
import android.view.View
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import dev.persona.watch.CredentialStore
import dev.persona.watch.HubDirectory
import dev.persona.watch.HubGroupReader
import dev.persona.watch.R
import dev.persona.watch.data.HubException
import dev.persona.watch.ui.WatchTheme as T
import java.text.DateFormat
import java.util.Date
import java.util.concurrent.Executors

/** A glanceable companion for everyone. Only this visible page animates the original Milk Frog. */
class PetActivity : Activity() {
    private val handler = Handler(Looper.getMainLooper())
    private val worker = Executors.newSingleThreadExecutor()
    private val connections = Executors.newFixedThreadPool(4)
    private val page = PetPageState()
    private var pack: PetPack? = null
    private val manifest get() = page.manifest
    private val status get() = page.stored.status
    private val updatedAt get() = page.stored.updatedAt
    private var foreground = false
    private var busy = false
    @Volatile private var playbackGeneration = 0
    private var frames = emptyList<Bitmap>()
    private var face: Bitmap? = null
    private var clipName = ""
    private var clipStart = 0L
    private lateinit var art: ImageView
    private lateinit var stateLabel: TextView
    private lateinit var dateLabel: TextView
    private lateinit var notice: TextView
    private val poll = PetForegroundPoll(foreground = { foreground }, interactive = ::interactive,
        refresh = { refresh(false) }, schedule = { task, delay -> handler.postDelayed(task, delay) })
    private val expire = Runnable {
        if (foreground) {
            val changed = showState()
            page.connection?.let { connection -> page.publication()?.let { PetBridge.publish(this, connection, it.status, it.updatedAt) } }
            if (changed) play(status.state.wire)
        }
    }
    private val animate = object : Runnable {
        override fun run() {
            if (!foreground) return
            if (!motionEnabled()) { play(status.state.wire); return }
            val clip = manifest?.clips?.get(clipName)
            if (interactive() && motionEnabled() && clip != null && frames.isNotEmpty()) {
                val index = ((SystemClock.elapsedRealtime() - clipStart) * clip.fps / 1000).toInt()
                if (clipName == "tap" && index >= frames.size) { play(status.state.wire); return }
                art.setImageBitmap(frames[index % frames.size])
                handler.postDelayed(this, (1000 / clip.fps).toLong())
            } else {
                art.setImageBitmap(face)
                handler.postDelayed(this, PetTokens.stillCheckMs)
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = T.background; window.navigationBarColor = T.background
        val body = T.column(this).apply {
            gravity = Gravity.CENTER_HORIZONTAL
            setPadding(dp(T.screenInset), dp(T.roundEndInset), dp(T.screenInset), dp(T.roundEndInset))
        }
        body.addView(T.text(this, getString(R.string.pet_title), T.title, bold = true).apply { isAccessibilityHeading = true })
        art = ImageView(this).apply {
            layoutParams = LinearLayout.LayoutParams(dp(PetTokens.artSizeDp), dp(PetTokens.artSizeDp))
            scaleType = ImageView.ScaleType.FIT_CENTER
            contentDescription = getString(R.string.pet_tap)
            isClickable = true
            setOnClickListener {
                if (manifest != null) { performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK); play(if (status.state == PetState.IDLE) "tap" else status.state.wire) }
            }
        }
        body.addView(art)
        stateLabel = T.text(this, getString(R.string.loading), T.heading).apply { gravity = Gravity.CENTER }
        dateLabel = T.text(this, "", T.caption, T.secondary).apply { gravity = Gravity.CENTER }
        notice = T.text(this, "", T.body, T.secondary).apply { gravity = Gravity.CENTER }
        body.addView(stateLabel); body.addView(dateLabel); body.addView(notice)
        body.addView(T.button(this, getString(R.string.retry)) { refresh(true) })
        body.addView(T.button(this, getString(R.string.back)) { finish() })
        setContentView(ScrollView(this).apply { setBackgroundColor(T.background); addView(body) })
    }

    override fun onResume() {
        super.onResume(); foreground = true
        val connection = runCatching { CredentialStore(this).load()?.validated() }.getOrNull()
        val empty = PetStoredStatus(PetStatus(PetState.OFFLINE), 0)
        val saved = connection?.let { runCatching { PetBridge.read(this, it) }.getOrDefault(empty) } ?: empty
        if (page.resume(connection, saved, System.currentTimeMillis())) {
            playbackGeneration++
            busy = false
            pack = connection?.let { PetPack(cacheDir, it) }
            clearArtwork()
            notice.text = ""
        }
        showState()
        if (connection == null) {
            notice.text = getString(R.string.pet_pair_first)
            return
        }
        val source = pack ?: PetPack(cacheDir, connection).also { pack = it }
        val ticket = page.generation
        if (manifest == null) worker.execute {
            val cached = source.cached()
            handler.post {
                if (!isDestroyed && foreground && cached != null && page.acceptManifest(ticket, cached)) {
                    showState(); play(status.state.wire)
                }
            }
        } else play(status.state.wire)
        refresh(manifest == null)
        handler.removeCallbacks(poll)
        handler.postDelayed(poll, PetTokens.pollMs)
    }
    override fun onPause() {
        foreground = false
        page.invalidate(); playbackGeneration++; busy = false
        handler.removeCallbacks(poll); handler.removeCallbacks(animate); handler.removeCallbacks(expire)
        art.setImageBitmap(face)
        super.onPause()
    }
    override fun onDestroy() {
        page.invalidate(); playbackGeneration++; foreground = false
        handler.removeCallbacks(poll); handler.removeCallbacks(animate); handler.removeCallbacks(expire)
        // Let already-posted decode completions run their stale-generation bitmap cleanup.
        worker.shutdownNow(); connections.shutdownNow()
        clearArtwork()
        super.onDestroy()
    }

    private fun refresh(fetchAssets: Boolean) {
        val connection = page.connection ?: return
        val source = pack ?: return
        if (busy || !foreground) return
        val ticket = page.generation
        val previousGroups = page.groups
        busy = true
        if (manifest == null) notice.text = getString(R.string.pet_loading)
        worker.execute {
            if (!page.accepts(ticket)) return@execute
            val assets = readPetAssets(fetchAssets, cached = { source.cached() }, fetch = source::fetch)
            if (!page.accepts(ticket)) return@execute
            var groupCount = 1
            val read = runCatching {
                val extras = HubDirectory(applicationContext).additional()
                groupCount += extras.size
                HubGroupReader.read(connection, extras, previousGroups,
                    resources.configuration.locales[0].toLanguageTag(), connections)
            }
            val result = read.getOrNull()
            val reachability = PetReachability.from(result, groupCount)
            val allFailed = reachability.allFailed
            val partlyFailed = reachability.partlyFailed
            handler.post {
                if (isDestroyed || !foreground || !page.accepts(ticket)) return@post
                busy = false
                val previousStatus = status
                val missingManifest = manifest == null
                page.acceptRefresh(ticket, assets.manifest, result, groupCount, System.currentTimeMillis())
                val expired = showState()
                page.publication()?.let { PetBridge.publish(applicationContext, connection, it.status, it.updatedAt) }
                notice.text = when {
                    manifest == null && assets.failure.let { it is HubException && it.status in listOf(403, 404) } -> getString(R.string.pet_unsupported)
                    manifest == null -> getString(R.string.pet_unavailable_body)
                    allFailed -> getString(R.string.pet_retry_body)
                    partlyFailed -> getString(R.string.hub_partial)
                    else -> ""
                }
                if (expired || previousStatus != status || missingManifest && manifest != null || face == null) play(status.state.wire)
            }
        }
    }

    private fun showState(): Boolean {
        val now = System.currentTimeMillis()
        val changed = page.revalidate(now)
        stateLabel.text = if (manifest == null) getString(R.string.pet_unavailable) else PetPresentation.title(this, status)
        dateLabel.text = if (updatedAt > 0) getString(R.string.pet_updated, DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(updatedAt))) else ""
        art.visibility = if (manifest == null) View.GONE else View.VISIBLE
        handler.removeCallbacks(expire)
        if (foreground) page.expiryDelay(now)?.let { handler.postDelayed(expire, it) }
        return changed
    }

    private fun play(name: String) {
        val current = manifest ?: return
        val source = pack ?: return
        val currentGeneration = ++playbackGeneration
        val ticket = page.generation
        val currentState = status.state
        val plan = PetPlayback.plan(currentState, name, motionEnabled(), interactive())
        clipName = plan.clip
        handler.removeCallbacks(animate)
        worker.execute {
            if (isDestroyed || !page.accepts(ticket) || currentGeneration != playbackGeneration) return@execute
            val activePlan = PetPlayback.plan(currentState, name, plan.animated && motionEnabled(), interactive())
            val artwork = activePlan.load(
                still = { state -> current.faces[state]?.let { runCatching { PetPresentation.face(source.file(it)) }.getOrNull() } },
                frames = { clipName ->
                    if (!page.accepts(ticket) || currentGeneration != playbackGeneration || !motionEnabled()) emptyList()
                    else current.clips[clipName]?.let { clip ->
                        runCatching { PetAnimation.decode(source.file(clip.asset).readBytes(), clip.frames) }.getOrDefault(emptyList())
                    }.orEmpty()
                })
            val still = artwork.still
            val decoded = artwork.frames
            handler.post {
                if (isDestroyed || !foreground || !page.accepts(ticket) || currentGeneration != playbackGeneration) {
                    still?.recycle()
                    decoded.forEach { it.recycle() }
                    return@post
                }
                val previousFace = face
                val previousFrames = frames
                face = still; frames = decoded; clipName = activePlan.clip; clipStart = SystemClock.elapsedRealtime()
                art.setImageBitmap(still)
                previousFace?.let { if (!it.isRecycled) it.recycle() }
                previousFrames.forEach { if (!it.isRecycled) it.recycle() }
                if (activePlan.animated && motionEnabled()) handler.post(animate)
                else { frames.forEach { if (!it.isRecycled) it.recycle() }; frames = emptyList() }
            }
        }
    }

    private fun clearArtwork() {
        handler.removeCallbacks(animate)
        art.setImageDrawable(null)
        frames.forEach { if (!it.isRecycled) it.recycle() }
        face?.let { if (!it.isRecycled) it.recycle() }
        frames = emptyList(); face = null; clipName = ""
    }

    private fun interactive() = getSystemService(PowerManager::class.java).isInteractive
    private fun motionEnabled() = Settings.Global.getFloat(contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) > 0f
    private fun dp(value: Int) = T.dp(this, value)
}
