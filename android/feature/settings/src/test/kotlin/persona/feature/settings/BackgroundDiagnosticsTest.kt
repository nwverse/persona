package persona.feature.settings

import android.app.ApplicationExitInfo
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.util.ReflectionHelpers

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh")
class BackgroundDiagnosticsTest {
    private val processName = "persona.test"
    private val resources get() = ApplicationProvider.getApplicationContext<Context>().resources
    private fun snapshot(exits: List<BackgroundExit>?) = BackgroundDiagnostics("0.2.9 (12)", "15 (API 35)",
        "Test manufacturer", "Test model", batteryExempt = null, backgroundRestricted = null, exits = exits)

    @Test fun userRequestedRemainsNeutralAndUserStoppedMeansTheAndroidUser() {
        val requested = BackgroundExit(1_000, ApplicationExitInfo.REASON_USER_REQUESTED, 0, 400, 0, 0).text(resources)
        assertTrue(requested.contains("reason=10 (用户操作或任务移除，无法细分)"))
        assertFalse(requested.contains("被你停止"))
        val userStopped = BackgroundExit(1_000, ApplicationExitInfo.REASON_USER_STOPPED, 0, 400, 0, 0).text(resources)
        assertTrue(userStopped.contains("reason=11 (Android 用户已停止（多用户设备）)"))
        assertFalse(userStopped.contains("任务移除"))
    }

    @Test fun onlyTheThreeNewestNumericRecordsReachTheCopiedReport() {
        val source = listOf(exit(1_000, ApplicationExitInfo.REASON_ANR), exit(4_000, ApplicationExitInfo.REASON_CRASH),
            exit(3_000, ApplicationExitInfo.REASON_USER_REQUESTED), exit(2_000, ApplicationExitInfo.REASON_LOW_MEMORY),
            exit(5_000, ApplicationExitInfo.REASON_CRASH, "persona.test:webview"))
        source.forEach { ReflectionHelpers.setField(it, "mDescription", "secret-token https://private.example chat payload") }
        val records = requireNotNull(readBackgroundExits(processName) { source })
        assertEquals(listOf(4_000L, 3_000L, 2_000L), records.map { it.timestamp })
        val report = snapshot(records).copyText(resources)
        assertTrue(report.contains("timestamp=4000"))
        assertTrue(report.contains("reason=4 (App 崩溃)"))
        assertTrue(report.contains("status=9 importance=400 PSS=123 kB RSS=456 kB"))
        assertFalse(report.contains("timestamp=1000"))
        listOf("secret-token", "private.example", "chat payload", "description=", "trace=", "persona.test", "webview", "timestamp=5000").forEach {
            assertFalse("Unexpected private field: $it", report.contains(it))
        }
    }

    @Test fun noHistoryAndAccessFailureAreExplicitAndNeverReportAnExitAsSuccess() {
        val empty = readBackgroundExits(processName) { emptyList() }
        assertEquals(emptyList<BackgroundExit>(), empty)
        val emptyReport = snapshot(empty).copyText(resources)
        assertTrue(emptyReport.contains("未获取：系统最近记录中没有主进程退出"))
        assertFalse(emptyReport.contains("reason="))
        val failed = readBackgroundExits(processName) { throw SecurityException("secret-token https://private.example") }
        assertNull(failed)
        val failedReport = snapshot(failed).copyText(resources)
        assertTrue(failedReport.contains("未获取：无法读取"))
        assertFalse(failedReport.contains("secret-token"))
        assertFalse(failedReport.contains("private.example"))
        assertFalse(failedReport.contains("reason="))
    }

    @Test fun unknownReasonAndZeroSamplesStayRawRatherThanInventingACause() {
        val report = snapshot(listOf(BackgroundExit(1_000, 987, 9, 400, 0, 0))).copyText(resources)
        assertTrue(report.contains("reason=987 (未知或未识别的系统原因)"))
        assertTrue(report.contains("PSS=0 kB RSS=0 kB"))
        assertFalse(report.contains("系统报告低内存"))
    }

    @Test fun unavailableSystemSettingsDoNotBecomeExemptOrUnrestricted() {
        val unknown = snapshot(null).rows(resources).toMap()
        assertEquals("未获取：无法读取", unknown["电池优化豁免"])
        assertEquals("未获取：无法读取", unknown["系统后台限制"])
        val known = snapshot(null).copy(batteryExempt = false, backgroundRestricted = true).rows(resources).toMap()
        assertEquals("未豁免", known["电池优化豁免"])
        assertEquals("受限制", known["系统后台限制"])
    }

    private fun exit(timestamp: Long, reason: Int, process: String = processName): ApplicationExitInfo =
        ReflectionHelpers.newInstance(ApplicationExitInfo::class.java).apply {
            ReflectionHelpers.setField(this, "mProcessName", process)
            ReflectionHelpers.setField(this, "mTimestamp", timestamp)
            ReflectionHelpers.setField(this, "mReason", reason)
            ReflectionHelpers.setField(this, "mStatus", 9)
            ReflectionHelpers.setField(this, "mImportance", 400)
            ReflectionHelpers.setField(this, "mPss", 123L)
            ReflectionHelpers.setField(this, "mRss", 456L)
        }
}
