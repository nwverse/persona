package persona.android.ui

import androidx.compose.runtime.*
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import kotlinx.serialization.json.*
import persona.android.R
import persona.android.data.Hub
import persona.core.designsystem.JDialog

/** The scanned code names a request on the already paired hub, never a server to send credentials to. */
@Composable
fun BrowserLoginApproval(hub: Hub, link: String, onDismiss: () -> Unit) {
    var name by remember(link) { mutableStateOf<String?>(null) }
    var error by remember(link) { mutableStateOf(false) }
    var busy by remember(link) { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val request = remember(link, hub) { browserLoginRequest(link, hub.connection.http.hubId) }
    suspend fun send(allow: Boolean? = null): JsonObject {
        val body = buildJsonObject { put("id", request!!.id); put("hub", request.hub); allow?.let { put("allow", it) } }
        return Json.parseToJsonElement(hub.api.raw("POST", "/api/v1/auth/browser-login/approve", body.toString())).jsonObject
    }
    LaunchedEffect(hub, link) {
        if (request == null) { error = true; return@LaunchedEffect }
        try { name = send().getValue("name").jsonPrimitive.content }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { error = true }
    }
    fun decide(allow: Boolean) {
        if (busy) return
        if (name == null) { onDismiss(); return }
        busy = true
        scope.launch {
            try { send(allow); onDismiss() }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { error = true }
            finally { busy = false }
        }
    }
    JDialog(
        onDismiss = { if (!busy) decide(false) },
        title = stringResource(R.string.app_browser_login_title),
        text = when {
            error -> stringResource(R.string.app_browser_login_failed)
            name == null -> stringResource(R.string.app_browser_login_loading)
            else -> stringResource(R.string.app_browser_login_details, name!!)
        },
        confirm = stringResource(R.string.app_browser_login_allow) to { decide(true) },
        dismiss = stringResource(R.string.app_browser_login_cancel) to { decide(false) },
        confirmEnabled = name != null && !busy && !error,
    )
}
