package persona.android.assist

import android.content.Context
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import persona.android.PersonaApp
import persona.android.R
import persona.android.di.ConnectionRuntime
import persona.android.ui.AppServices
import persona.android.ui.ShellSession
import persona.core.contracts.AgentState
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.ConnectionState
import persona.core.domain.ComposerMessage
import persona.feature.chat.ChatAppBindings
import persona.feature.chat.ChatLinkContext
import persona.feature.chat.ChatWiringSource
import persona.feature.chat.ComposerAttachment
import persona.feature.chat.uploadAttachment

@EntryPoint
@InstallIn(SingletonComponent::class)
internal interface AssistOverlayEntryPoint {
    fun assistAppServices(): AppServices
    fun assistChatWiring(): ChatWiringSource
    fun assistConnections(): ConnectionRuntime
}

/**
 * Connects [AssistOverlayController] to the app's existing Hub connection, main conversation
 * repository, attachment uploader, and live streaming transcript without opening any extra socket.
 */
@OptIn(ExperimentalCoroutinesApi::class)
internal class AssistHubBridge(
    private val context: Context,
    private val scope: CoroutineScope,
    private val onSnapshot: (AssistHubSnapshot) -> Unit,
) {
    private val appContext = context.applicationContext
    private val app: PersonaApp? = appContext as? PersonaApp
    private val entryPoint: AssistOverlayEntryPoint? = runCatching {
        EntryPointAccessors.fromApplication(appContext, AssistOverlayEntryPoint::class.java)
    }.getOrNull()

    private var observeJob: Job? = null

    fun wakeHub() {
        runCatching { entryPoint?.assistConnections()?.wake() }
    }

    fun start() {
        if (observeJob?.isActive == true) return
        val services = entryPoint?.assistAppServices()
        val wiring = entryPoint?.assistChatWiring()
        if (services == null || wiring == null) {
            onSnapshot(AssistHubSnapshot(status = AssistHubStatus.Unpaired))
            return
        }
        wakeHub()
        val pairingReady = app?.pairingReady ?: kotlinx.coroutines.flow.MutableStateFlow(true)
        val pairingError = app?.pairingRestoreError ?: kotlinx.coroutines.flow.MutableStateFlow(false)
        observeJob = scope.launch {
            combine(pairingReady, pairingError, services.session, wiring.current) { ready, err, session, binding ->
                HubBindingTuple(ready, err, session, binding)
            }.collectLatest { tuple ->
                val session = tuple.session
                if (session == null) {
                    val status = if (!tuple.pairingReady && !tuple.pairingError) {
                        AssistHubStatus.Connecting
                    } else {
                        AssistHubStatus.Unpaired
                    }
                    onSnapshot(AssistHubSnapshot(status = status))
                    return@collectLatest
                }
                val activeBinding = tuple.binding?.takeIf { it.scope.path == session.identity }
                val connStateFlow = (activeBinding?.connection ?: session.connection).state
                val mainSessionFlow = session.assistant.assistant.map { it.mainSession }.distinctUntilChanged()
                val chatFlow = mainSessionFlow.flatMapLatest { key ->
                    if (key == null) flowOf<Pair<SessionKey?, ChatSnapshot?>>(null to null)
                    else session.chats.observe(key).map { key to it }
                }
                val outboxFlow = activeBinding?.mainSender?.outbox ?: session.sender.outbox
                combine(
                    connStateFlow,
                    session.assistant.assistant,
                    session.sessions.sessions,
                    chatFlow,
                    outboxFlow,
                ) { connState, assistant, sessions, observedChat, outbox ->
                    val mainKey = assistant.mainSession
                    val mainSessionRow = mainKey?.let { key ->
                        sessions.firstOrNull { it.key == key }
                            ?: assistant.conversations.firstOrNull { it.key == key }
                    }
                    val working = mainSessionRow?.foldedState == AgentState.Working ||
                        mainSessionRow?.status == "working"
                    val chat = observedChat.second?.takeIf { observedChat.first == mainKey }
                    val hubStatus = mapConnectionStateToHubStatus(
                        connectionState = connState,
                        hasBinding = activeBinding != null || session.demo != null,
                    )
                    AssistHubSnapshot(
                        status = hubStatus,
                        mainSession = mainKey,
                        mainWorking = working,
                        chatSnapshot = chat,
                        outbox = outbox,
                    )
                }.collect { snapshot ->
                    onSnapshot(snapshot)
                }
            }
        }
    }

    fun stop() {
        observeJob?.cancel()
        observeJob = null
    }

    suspend fun sendTextToMain(prompt: String, clientMessageId: String) {
        val (session, binding) = requireReadySession()
        val mainKey = session.assistant.assistant.value.mainSession
        val wireText = formatAssistWireMessage(
            prompt = prompt,
            remoteImagePath = null,
            contextCard = null,
            photosOnlyPrompt = "",
        )
        if (mainKey != null) {
            session.sender.send(mainKey, wireText, clientMessageId)
        } else {
            val mainSender = binding?.mainSender
                ?: error(context.getString(R.string.app_assist_hub_offline_title))
            mainSender.sendToMain(wireText, clientMessageId)
        }
    }

    suspend fun sendWithScreenToMain(
        prompt: String,
        screenshot: ComposerAttachment,
        contextCard: ChatLinkContext?,
        clientMessageId: String,
    ) {
        val (session, binding) = requireReadySession()
        val activeBinding = binding ?: error(context.getString(R.string.app_assist_hub_offline_title))
        val targetKey = session.assistant.assistant.value.mainSession
            ?: activeBinding.mainUploadTarget?.invoke()
            ?: run {
                try {
                    session.assistant.startConversation()
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (_: Exception) {
                    null
                }
            }
            ?: error(context.getString(R.string.app_assist_hub_offline_title))
        val remotePath = uploadAttachment(
            context = appContext,
            service = activeBinding.composer,
            session = targetKey,
            attachment = screenshot,
        )
        val wireText = formatAssistWireMessage(
            prompt = prompt,
            remoteImagePath = remotePath,
            contextCard = contextCard,
            photosOnlyPrompt = context.getString(R.string.app_assist_screen_only_prompt),
        )
        session.sender.send(targetKey, wireText, clientMessageId)
    }

    private fun requireReadySession(): Pair<ShellSession, ChatAppBindings?> {
        val services = entryPoint?.assistAppServices()
            ?: error(context.getString(R.string.app_assist_hub_unpaired_title))
        val wiring = entryPoint.assistChatWiring()
        val session = services.session.value
            ?: error(context.getString(R.string.app_assist_hub_unpaired_title))
        val binding = wiring.current.value?.takeIf { it.scope.path == session.identity }
        val connState = (binding?.connection ?: session.connection).state.value
        if (connState != ConnectionState.Online) {
            error(context.getString(R.string.app_assist_hub_offline_title))
        }
        return session to binding
    }

    private data class HubBindingTuple(
        val pairingReady: Boolean,
        val pairingError: Boolean,
        val session: ShellSession?,
        val binding: ChatAppBindings?,
    )
}

internal fun mapConnectionStateToHubStatus(
    connectionState: ConnectionState,
    hasBinding: Boolean,
): AssistHubStatus = when (connectionState) {
    ConnectionState.Online -> if (hasBinding) AssistHubStatus.Ready else AssistHubStatus.Connecting
    ConnectionState.Connecting, ConnectionState.Updating -> AssistHubStatus.Connecting
    ConnectionState.WaitingForNetwork,
    ConnectionState.NeedsLocalNetworkPermission,
    ConnectionState.CertificateMismatch,
    ConnectionState.Revoked,
    -> AssistHubStatus.Offline
}

internal fun formatAssistWireMessage(
    prompt: String,
    remoteImagePath: String?,
    contextCard: ChatLinkContext?,
    photosOnlyPrompt: String,
): String {
    val trimmed = prompt.trim()
    val body = if (remoteImagePath != null) {
        ComposerMessage.text(
            words = trimmed,
            photos = listOf(remoteImagePath),
            files = emptyList(),
            photosOnly = photosOnlyPrompt,
        )
    } else {
        trimmed
    }
    return if (contextCard != null) {
        "${contextCard.xml()}\n\n$body"
    } else {
        body
    }
}
