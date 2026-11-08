package persona.feature.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner

/**
 * A secret typed into one card (a password), held only by this composition: never saved instance state, a view model
 * or a request draft. It starts empty for another [key] (another card), and empties when the app goes to the
 * background (ON_STOP), when the card leaves the composition and whenever [clearWhen] turns true (the card expired, an
 * answer's delivery is unknown). Callers empty it themselves once it is sent.
 */
@Composable
fun rememberTransientSecret(key: Any, clearWhen: Boolean): MutableState<String> {
    val secret = remember(key) { mutableStateOf("") }
    LaunchedEffect(key, clearWhen) { if (clearWhen) secret.value = "" }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, key) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_STOP) secret.value = "" }
        lifecycle.addObserver(observer)
        onDispose { secret.value = ""; lifecycle.removeObserver(observer) }
    }
    return secret
}
