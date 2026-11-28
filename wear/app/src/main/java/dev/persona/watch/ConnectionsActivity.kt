package dev.persona.watch

import android.app.Activity
import android.app.AlertDialog
import android.annotation.SuppressLint
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.text.method.PasswordTransformationMethod
import android.view.View
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import dev.persona.watch.data.HubClient
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.PairingInput
import dev.persona.watch.ui.WatchTheme as T
import java.util.concurrent.Executors

class ConnectionsActivity : Activity() {
    private val io = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())
    private lateinit var directory: HubDirectory
    private lateinit var body: LinearLayout
    private var error = ""
    private var busy = false
    private var recoveryFailed = false
    private var groups = emptyList<SavedHub>()
    private var primaryUrl: String? = null
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState); directory = HubDirectory(this); reload()
    }
    private fun reload(removalFailed: Boolean = false, notice: String = "") {
        busy = true
        render()
        io.execute {
            val recovery = runCatching { directory.recoverPending() }
            val saved = runCatching { directory.additional() to CredentialStore(this).load()?.baseUrl }
            main.post {
                if (!isDestroyed) {
                    val feedback = connectionReloadFeedback(removalFailed, recovery, saved.isFailure)
                    recoveryFailed = feedback.retry
                    error = when (feedback.notice) {
                        ConnectionReloadNotice.FAILURE -> getString(R.string.action_failed)
                        ConnectionReloadNotice.PENDING -> getString(R.string.hub_add_pending)
                        ConnectionReloadNotice.RECOVERED -> getString(R.string.hub_added)
                        ConnectionReloadNotice.NONE -> notice
                    }
                    saved.onSuccess { (rows, primary) -> groups = rows; primaryUrl = primary }
                    busy = false; render()
                }
            }
        }
    }
    private fun render() {
        val scroll = ScrollView(this).apply { setBackgroundColor(T.background) }
        body = T.column(this).apply { setPadding(dp(T.screenInset), dp(T.roundEndInset), dp(T.screenInset), dp(T.roundEndInset)) }
        scroll.addView(body); setContentView(scroll)
        body.addView(T.text(this, getString(R.string.hub_groups), T.title, bold = true))
        body.addView(T.text(this, getString(R.string.hub_primary), T.caption, T.secondary))
        if (error.isNotEmpty()) body.addView(T.text(this, error, T.caption, T.working))
        if (recoveryFailed) button(R.string.retry) { reload() }
        groups.forEach { group ->
            body.addView(T.text(this, group.name, T.heading, bold = true))
            button(R.string.hub_remove) {
                AlertDialog.Builder(this).setMessage(R.string.hub_remove_body).setNegativeButton(R.string.cancel, null)
                    .setPositiveButton(R.string.hub_remove) { _, _ ->
                        busy = true; render()
                        io.execute {
                            val result = runCatching { directory.remove(group.id) }
                            main.post {
                                if (!isDestroyed) {
                                    completeConnectionRemoval(result,
                                        showError = { error = getString(R.string.action_failed) },
                                        reload = { reload(removalFailed = result.isFailure) })
                                }
                            }
                        }
                    }.show()
            }
        }
        if (groups.size < 3) button(R.string.hub_add) { form() }
        button(R.string.back) { finish() }
    }
    // One-time self-hosted pairing credential, not an account password; never saved before exchange.
    @SuppressLint("WearPasswordInput")
    private fun form() {
        body.removeAllViews()
        body.addView(T.text(this, getString(R.string.hub_add), T.title, bold = true))
        val name = input(R.string.hub_name)
        val address = input(R.string.pair_link_or_address)
        val token = input(R.string.pair_token).apply {
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            transformationMethod = PasswordTransformationMethod.getInstance()
        }
        button(R.string.connect) {
            val title = name.text.toString().trim()
            val config = runCatching { PairingInput.parse(address.text.toString(), token.text.toString()) }.getOrNull()
            if (config == null || title.isEmpty() || title.length > 80) { error = getString(R.string.invalid_connection); render(); return@button }
            if ((groups.mapNotNull { it.config?.baseUrl } + primaryUrl).any { HubConfig.sameOrigin(it, config.baseUrl) }) {
                error = getString(R.string.hub_duplicate); render(); return@button
            }
            busy = true
            for (index in 0 until body.childCount) body.getChildAt(index).isEnabled = false
            body.addView(T.text(this, getString(R.string.pairing), T.caption))
            io.execute {
                val result = runCatching {
                    directory.add(title, config) { HubClient(it).pair() }
                }
                main.post { if (!isDestroyed) {
                    error = getString(if (result.isSuccess) R.string.hub_added else R.string.hub_add_failed)
                    reload(notice = error)
                } }
            }
        }
        button(R.string.cancel) { render() }
    }
    private fun input(label: Int): EditText {
        val field = EditText(this).apply {
            id = View.generateViewId()
            hint = getString(label)
            minHeight = dp(T.touch)
            minimumHeight = dp(T.touch)
            setTextColor(T.ink); setHintTextColor(T.secondary); textSize = T.body
        }
        body.addView(T.text(this, getString(label), T.caption, T.secondary).apply { labelFor = field.id })
        body.addView(field)
        return field
    }
    private fun button(label: Int, action: () -> Unit) {
        body.addView(T.button(this, getString(label)) { if (!busy) action() }.apply { isEnabled = !busy })
    }
    private fun dp(value: Int) = T.dp(this, value)
    override fun onDestroy() { io.shutdown(); main.removeCallbacksAndMessages(null); super.onDestroy() }
}
