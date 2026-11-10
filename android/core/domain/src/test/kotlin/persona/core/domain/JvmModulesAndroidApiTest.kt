package persona.core.domain

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The plain JVM modules (core:contracts, core:domain, core:hub, core:terminal) compile against the JDK, not android.jar:
 * neither lint's NewApi check nor desugaring sees them, so a java.time call newer than the app's minSdk (30) builds
 * and then throws NoSuchMethodError on the phone. LocalDate.ofInstant (Android 34) crashed the app on Android 12 and
 * HarmonyOS 4 as soon as the main conversation's 今天 card was asked for. These calls have older equivalents.
 */
class JvmModulesAndroidApiTest {
    private val newer = mapOf(
        "LocalDate.ofInstant(" to "instant.atZone(zone).toLocalDate()",
        "LocalTime.ofInstant(" to "instant.atZone(zone).toLocalTime()",
        ".toEpochSecond(LocalTime" to "atStartOfDay(zone).toEpochSecond()",
        ".toDaysPart()" to "toDays()",
        ".toHoursPart()" to "toHours() % 24",
        ".toMinutesPart()" to "toMinutes() % 60",
        ".toSecondsPart()" to "seconds % 60",
        ".dividedBy(Duration" to "toMillis() / other.toMillis()",
    )

    @Test fun noJavaTimeCallNewerThanTheAppsMinimumAndroid() {
        val root = File(System.getProperty("persona.repo.root") ?: "../..", "android")
        val found = listOf("contracts", "domain", "hub", "terminal").flatMap { module ->
            File(root, "core/$module/src/main").walkTopDown().filter { it.isFile && it.extension == "kt" }.flatMap { file ->
                file.readLines().withIndex().flatMap { (n, line) ->
                    newer.keys.filter { it in line.substringBefore("//") }.map { "${file.relativeTo(root)}:${n + 1}: $it (use ${newer[it]})" }
                }
            }.toList()
        }
        assertEquals("java.time calls Android adds after API 30", emptyList<String>(), found)
    }
}
