package persona.feature.settings

import android.content.res.Resources
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.ExtensionAuth
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

// An extension's sign-in (#4507): the "Sign in" section of its page and the sheet that signs it in, a step at a time. The
// steps are a connector's (fields, a device code, a QR code), drawn by ConnectorSignInStep and driven by
// ConnectorSignInModel over the extension's own routes.

internal object ExtensionAuthText {
    /** Not signed in / Waiting for confirmation / Signed in: <account> / Expired / Error: <reason> / Extension not running. */
    fun state(res: Resources, auth: ExtensionAuth): String = when (auth.state) {
        ExtensionAuth.SignedOut -> res.getString(R.string.settings_ext_auth_signed_out)
        ExtensionAuth.Pending -> res.getString(R.string.settings_ext_auth_pending)
        ExtensionAuth.SignedIn -> auth.account?.takeIf { it.isNotBlank() }?.let { res.getString(R.string.settings_ext_auth_signed_in_as, it) }
            ?: res.getString(R.string.settings_ext_auth_signed_in)
        ExtensionAuth.Expired -> res.getString(R.string.settings_ext_auth_expired)
        ExtensionAuth.Error -> auth.message?.takeIf { it.isNotBlank() }?.let { res.getString(R.string.settings_ext_auth_error_detail, it) }
            ?: res.getString(R.string.settings_ext_auth_error)
        else -> res.getString(R.string.settings_ext_auth_unknown)
    }

    fun status(auth: ExtensionAuth): Status = when (auth.state) {
        ExtensionAuth.SignedIn -> Status.Unread
        ExtensionAuth.Pending -> Status.Working
        ExtensionAuth.SignedOut, ExtensionAuth.Expired, ExtensionAuth.Error -> Status.NeedsYou
        else -> Status.Offline
    }

    fun title(res: Resources, auth: ExtensionAuth): String = auth.title.ifBlank { res.getString(R.string.settings_ext_auth_default_title) }

    /** "Sign in again" once it has been signed in (now, or before it expired); "Sign in" otherwise. */
    fun signInLabel(res: Resources, auth: ExtensionAuth): String = res.getString(
        if (auth.state == ExtensionAuth.SignedIn || auth.state == ExtensionAuth.Expired) R.string.settings_ext_auth_sign_in_again
        else R.string.settings_ext_auth_sign_in)
}

/**
 * "Sign in" under the extension's state: how its sign-in is, "Sign in" ("Sign in again") and, signed in, "Sign out". [starting] is the first
 * step on its way; [error] why it did not come.
 */
internal fun LazyListScope.extensionSignIn(
    auth: ExtensionAuth, enabled: Boolean, starting: Boolean, error: String?, busy: Boolean, onSignIn: () -> Unit, onSignOut: () -> Unit,
) {
    item("auth-header") { JSectionHeader(stringResource(R.string.settings_ext_auth_header)) }
    item("auth-state") {
        val res = LocalResources.current
        Column {
            JGroupItem(if (auth.signedIn) GroupPosition.of(0, 2) else GroupPosition.Single) {
                JRow(ExtensionAuthText.title(res, auth), subtitle = ExtensionAuthText.state(res, auth), subtitleLines = 3, leadingIcon = PhIcons.UserCircle,
                    trailing = { JStatusDot(ExtensionAuthText.status(auth)) })
            }
            // As on a connector's page: signing out is a row of its own (it asks first), signing in a button.
            if (auth.signedIn) JGroupItem(GroupPosition.of(1, 2)) {
                JRow(stringResource(R.string.settings_ext_auth_sign_out), leadingIcon = PhIcons.X, destructive = true, chevron = false,
                    enabled = !busy && !starting, onClick = onSignOut)
            }
        }
    }
    item("auth-actions") {
        val res = LocalResources.current
        Column(Modifier.padding(top = Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JButton(ExtensionAuthText.signInLabel(res, auth), onSignIn, Modifier.fillMaxWidth(), icon = PhIcons.Key,
                kind = if (auth.signedIn) JButtonKind.Secondary else JButtonKind.Primary, loading = starting, enabled = enabled && !busy)
            error?.let { JInlineStatus(it, tone = InlineTone.Failed) }
        }
    }
    val note = if (!enabled) null else auth.description?.takeIf { it.isNotBlank() }
    if (!enabled) item("auth-off") {
        SettingsCaption(stringResource(R.string.settings_ext_auth_turn_on_first), Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
    }
    note?.let { item("auth-note") { SettingsCaption(it, Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s)) } }
}

/** The step of signing in the hub asks for now, as a sheet; closing it stops waiting. */
@Composable
internal fun ExtensionSignInSheet(auth: ExtensionAuth, name: String, bind: BindState, model: ConnectorSignInModel) {
    val prompt = bind.prompt ?: return
    val res = LocalResources.current
    JSheet(model::cancel, title = ExtensionAuthText.title(res, auth), dismissible = !bind.sending, pageActions = {}) {
        Column(Modifier.padding(vertical = Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            // The step's own hint says what to do; the extension's description only when it has none.
            auth.description?.takeIf { it.isNotBlank() && prompt.hint.isNullOrBlank() }?.let {
                SettingsCaption(it, Modifier.padding(horizontal = rowPadding))
            }
            ConnectorSignInStep(name, prompt, bind, model, deviceLink = true)
        }
    }
}
