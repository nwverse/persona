package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Subagent
import persona.core.contracts.WorkflowTool
import persona.core.domain.lines.SubagentStatus

/** A subagent's card and its grid say what iOS's do (`SubagentCardText`, `SubagentGridText.head`), in each language. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class SubagentCardTextTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private val agent = Subagent("a", toolUseId = "c", running = true, steps = 7, lastTool = WorkflowTool(name = "Bash", summary = "Running the tests"))

    @Test fun theCardSaysWhatItDoesThenHowItWent() {
        assertEquals("Running the tests… · 7 steps", SubagentCardText.line(res, SubagentStatus.Running, agent, null))
        assertEquals("Bash… · 1 step", SubagentCardText.line(res, SubagentStatus.Running, agent.copy(steps = 1, lastTool = WorkflowTool(name = "Bash")), null))
        assertEquals("Starting…", SubagentCardText.line(res, SubagentStatus.Running, agent.copy(lastTool = null), null))
        assertEquals("Starting…", SubagentCardText.line(res, SubagentStatus.Running, null, null))
        assertEquals("Done · 7 steps · 1 min", SubagentCardText.line(res, SubagentStatus.Done, agent, 80))
        assertEquals("Failed · 42 s", SubagentCardText.line(res, SubagentStatus.Failed, agent.copy(steps = null), 42))
        assertEquals("Stopped", SubagentCardText.line(res, SubagentStatus.Stopped, null, null))
    }

    @Test fun theGridSaysHowManyRunThenHowManyRanInHowManySteps() {
        assertEquals("Running 3 agents", SubagentCardText.head(res, 3, 3, 12))
        assertEquals("Running 1 agent", SubagentCardText.head(res, 3, 1, 12))
        assertEquals("Ran 3 agents · 12 steps", SubagentCardText.head(res, 3, 0, 12))
        assertEquals("Ran 2 agents", SubagentCardText.head(res, 2, 0, 0))
    }

    @Test @Config(qualifiers = "zh") fun inChinese() {
        assertEquals("正在启动…", SubagentCardText.line(res, SubagentStatus.Running, null, null))
        assertEquals("已完成 · 7 步 · 1 分", SubagentCardText.line(res, SubagentStatus.Done, agent, 80))
        assertEquals("3 个子 Agent 在运行", SubagentCardText.head(res, 3, 3, 0))
        assertEquals("跑了 3 个子 Agent · 共 12 步", SubagentCardText.head(res, 3, 0, 12))
    }

    @Test @Config(qualifiers = "ja") fun inJapanese() {
        assertEquals("起動中…", SubagentCardText.line(res, SubagentStatus.Running, null, null))
        assertEquals("サブエージェント 3 件が実行中", SubagentCardText.head(res, 3, 3, 0))
        assertEquals("サブエージェント 3 件を実行 · 計 12 ステップ", SubagentCardText.head(res, 3, 0, 12))
    }
}
