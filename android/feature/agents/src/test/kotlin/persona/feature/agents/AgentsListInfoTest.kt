package persona.feature.agents

import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.sort.*
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import java.time.Instant
import java.time.ZoneId

/** Robolectric for the strings: Simplified Chinese, the language the expected text is in. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
@OptIn(ExperimentalCoroutinesApi::class)
class AgentsListInfoTest {
    private val res get() = RuntimeEnvironment.getApplication().resources

    @Test fun sharedDraftUpdatesAndOutboxStatesRemainDistinct() = runTest {
        val session = Session("machine", "session", spaceName = "项目", slotName = "测试")
        val sessions = MutableStateFlow(listOf(session))
        val draft = MutableStateFlow(AgentDraft("继续检查"))
        var observed = emptyMap<SessionKey, AgentDraft>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            observeAgentDrafts(sessions, AgentsDraftSource { draft }).collect { observed = it }
        }
        assertEquals("继续检查", observed[session.key]?.text)
        draft.value = AgentDraft()
        assertEquals("", observed[session.key]?.text)
        draft.value = AgentDraft(hasAttachments = true)
        assertTrue(observed.getValue(session.key).hasAttachments)

        fun info(vararg states: DeliveryState) = AgentRowMetadata(draft = "保留输入").withDelivery(
            states.mapIndexed { index, state -> OutboxMessage("message-$index", session.key, "内容", state) }, false,
        )
        val mixed = info(DeliveryState.Unknown, DeliveryState.Queued, DeliveryState.WaitingForNetwork, DeliveryState.Sending)
        assertEquals(listOf("结果待确认", "连上后自动发送 · 2", "发送中"), mixed.deliveryLabels(res))
        assertTrue(mixed.hasDraft)
        assertEquals(listOf("正在核对"), info(DeliveryState.Verifying).deliveryLabels(res))
        assertTrue(info(DeliveryState.NeedsConfirmation).needsConfirmation)
        assertTrue(info(DeliveryState.Sent, DeliveryState.Refused, DeliveryState.Rejected).deliveryLabels(res).isEmpty())
        assertTrue(AgentRowMetadata().withDelivery(emptyList(), true).needsConfirmation)
        assertEquals("项目 › 测试 · 机器", session.spaceLocation(res, "机器"))
        assertEquals("循环，下次 17:20", LoopInfo(nextAt = "2026-01-01T17:20:00Z")
            .nextRunLabel(res, Instant.parse("2026-01-01T08:00:00Z"), ZoneId.of("UTC")))
        assertEquals("循环，下次时间待更新", LoopInfo(nextAt = "invalid")
            .nextRunLabel(res, Instant.parse("2026-01-01T08:00:00Z")))
        sessions.value = emptyList()
        assertTrue(observed.isEmpty())
    }

    @Test fun deliveryLabelsUseTheRowsResourcesRatherThanFixedChinese() {
        val context = RuntimeEnvironment.getApplication()
        val metadata = AgentRowMetadata(needsConfirmation = true, pendingCount = 2, sending = true)
        val expected = mapOf(
            "zh" to listOf("结果待确认", "连上后自动发送 · 2", "发送中"),
            "en" to listOf("Result unconfirmed", "Sends when connected · 2", "Sending"),
            "ja" to listOf("結果未確認", "接続後に自動送信 · 2", "送信中"),
        )
        for ((language, labels) in expected) {
            val config = android.content.res.Configuration(context.resources.configuration).apply {
                setLocale(java.util.Locale.forLanguageTag(language))
            }
            val resources = context.createConfigurationContext(config).resources
            assertEquals(language, labels, metadata.deliveryLabels(resources))
            assertEquals(resources.getString(persona.core.designsystem.R.string.designsystem_pending_verifying),
                metadata.copy(verifying = true).deliveryLabels(resources).first())
        }
    }

    @Test fun requestSummariesAndFiltersRespectSessionAndSpaceMutesAcrossGroups() {
        val base = Session("machine", "visible", state = "needs_you", spaceId = "space", spaceName = "项目", title = "检查接口")
        val sessions = listOf(base, base.copy(sessionId = "muted"), base.copy(sessionId = "space-muted", spaceName = "屏蔽空间"),
            base.copy(sessionId = "allowed", spaceName = "屏蔽空间"), base.copy(sessionId = "assistant", assistant = true),
            base.copy(machineId = "other", sessionId = "other"))
        val requests = sessions.map { Interaction(it.sessionId, it.machineId, it.sessionId, kind = "permission",
            summary = "完整命令\n" + "参数".repeat(100), expiresAt = if (it.sessionId == "visible") "2026-01-01T00:00:00Z" else null) }
        val mutes = AgentPreferences(spaces = listOf("machine/屏蔽空间"), sessions = listOf("machine/muted"), allowed = setOf("machine/allowed"))
        val machines = listOf(Machine("machine"), Machine("other"))
        AgentGrouping.entries.forEach { grouping ->
            val settings = AgentListSettings(grouping = grouping, machineFilter = "machine", query = "接口", quick = AgentQuickFilter.NeedsYou)
            val visible = visibleAgentRequests(requests, sessions, mutes, settings)
            assertEquals(listOf("visible", "allowed"), visible.map { it.id })
            val list = buildAgentList(sessions.map { AgentSession(it) }, machines, mutes = mutes, settings = settings)
            assertTrue(visible.all { request -> list.groups.flatMap { it.rows }.any { it.session.key == request.sessionKey } })
            assertEquals(4, list.counts[AgentQuickFilter.NeedsYou])
            assertTrue(visibleAgentRequests(requests, sessions, mutes, settings.copy(quick = AgentQuickFilter.Working)).isEmpty())
        }
        assertEquals(requests.first().summary, requests.first().cardSummary(res))
        val question = Interaction("question", kind = "question", questions = listOf(Question("第一个问题"), Question("第二个问题")))
        assertEquals("第一个问题\n\n第二个问题", question.cardSummary(res))
        assertEquals("完整计划", question.copy(kind = "plan", plan = "完整计划").cardSummary(res))
        assertEquals("屏幕问题\n完整说明", question.copy(kind = "screen", dialog = ScreenDialog(question = "屏幕问题", detail = "完整说明")).cardSummary(res))
    }

    @Test fun anotherAgentsCardSaysWhoAsks() {
        val claude = Interaction("ix", kind = "permission", toolName = "Bash")
        assertEquals("需要你授权", claude.cardTitle(res))
        assertEquals("Codex 需要你授权", claude.copy(agent = "codex").cardTitle(res))
        assertEquals("需要你授权", claude.copy(toolName = "mcp__persona__mail_send").cardTitle(res))
        assertEquals("DSH 在问你", claude.copy(kind = "question", agent = "dsh", agentLabelHint = "DSH").cardTitle(res))
        assertEquals("Claude 在问你", claude.copy(kind = "question", agent = "claude").cardTitle(res))
    }

    @Test fun requestCardsPreviewPlansWithoutMarkdownSyntax() {
        assertEquals("支持 HEIC\n1. 用 sips 转成 JPEG\n加粗说明", markdownPreview("## 支持 HEIC\n\n1. 用 `sips` 转成 JPEG\n**加粗**说明\n"))
        assertEquals("普通文字", markdownPreview("普通文字"))
    }
}
