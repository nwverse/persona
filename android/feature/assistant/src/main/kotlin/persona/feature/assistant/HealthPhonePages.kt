package persona.feature.assistant

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.flow.collect
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import java.text.NumberFormat
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

private enum class Trend(val key: String, val label: Int, val unit: Int, val get: (HealthDay) -> Double?) {
    Steps("steps", R.string.health_steps, R.string.health_count_steps, HealthDay::steps),
    Sleep("sleep", R.string.health_sleep, R.string.health_hours, HealthDay::sleep),
    Weight("weight", R.string.health_weight, R.string.health_kg, HealthDay::weight),
}

/** Health Connect resolves the pairing's enabled switch and Android grants again for each read. */
@Composable
internal fun HealthPhonePage(section: HealthSection, phone: PhoneHealthRecords?, onAccess: () -> Unit, onBack: () -> Unit) {
    var revision by remember { mutableIntStateOf(0) }
    LaunchedEffect(phone) { phone?.changes?.collect { revision++ } }
    var days by rememberSaveable { mutableIntStateOf(7) }
    var workoutId by remember(phone) { mutableStateOf<String?>(null) }
    val id = workoutId
    val back: () -> Unit = { if (id != null) workoutId = null else onBack() }
    BackHandler(id != null, back)
    val retry = { revision++; Unit }
    val title = stringResource(section.title)
    if (phone == null) {
        JListPage(title, onBack = onBack) { item { HealthFailure(HealthReadProblem.Unavailable, retry, onAccess) } }
    } else if (section == HealthSection.Trends) {
        val read = healthRead(phone, days, revision) { phone.trends(days) }
        JListPage(title, onBack = onBack, onRefresh = retry) {
            item { JSegmented(listOf(JSegment(stringResource(R.string.health_days, 7)), JSegment(stringResource(R.string.health_days, 30))),
                if (days == 7) 0 else 1, { days = if (it == 0) 7 else 30 }, Modifier.padding(vertical = Tokens.Space.s)) }
            when (read) {
                HealthRead.Loading -> item { JLoading() }
                is HealthRead.Failed -> item { HealthFailure(read.problem, retry, onAccess) }
                is HealthRead.Ready -> {
                    val trends = read.value
                    val visible = Trend.entries.filter { metric -> trends.days.any { metric.get(it) != null } }
                    if (visible.isEmpty()) item { JEmptyState(stringResource(R.string.health_no_measurements), stringResource(R.string.health_trends_empty_note), icon = PersonaIcons.healthTrends) }
                    items(visible, key = { it.key }) { metric ->
                        val locale = currentLocale
                        val values = trends.days.mapNotNull { day -> metric.get(day)?.let { dateText(day.date, locale) to it } }
                        val label = stringResource(metric.label)
                        val displays = values.map { (_, v) -> stringResource(metric.unit, number(v, locale)) }
                        HealthPlot(label, values.map { it.first }, values.map { it.second }, displays, metric == Trend.Steps)
                    }
                    if (trends.notGranted.isNotEmpty()) item {
                        JNotice(stringResource(R.string.health_partial), actionLabel = stringResource(R.string.health_manage_access), onAction = onAccess)
                    }
                    if (trends.failed.isNotEmpty()) item { HealthFailure(null, retry) }
                }
            }
        }
    } else if (id == null) {
        val read = healthRead(phone, revision) { phone.workouts() }
        JListPage(title, onBack = onBack, onRefresh = retry) {
            item { Text(stringResource(R.string.health_workout_note), Modifier.padding(vertical = Tokens.Space.m),
                style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
            when (read) {
                HealthRead.Loading -> item { JLoading() }
                is HealthRead.Failed -> item { HealthFailure(read.problem, retry, onAccess) }
                is HealthRead.Ready -> {
                    if (read.value.isEmpty()) item { JEmptyState(stringResource(R.string.health_no_workouts), stringResource(R.string.health_workouts_empty_note), icon = PersonaIcons.health) }
                    itemsIndexed(read.value, key = { _, workout -> workout.id }) { i, workout ->
                        JGroupItem(GroupPosition.of(i, read.value.size)) {
                            JRow(activity(workout.activity), subtitle = dateText(workout.start, currentLocale), leadingIcon = PersonaIcons.health,
                                value = stringResource(R.string.health_minutes, number(workout.duration / 60, currentLocale)),
                                onClick = { workoutId = workout.id })
                        }
                    }
                }
            }
        }
    } else {
        val read = healthRead(phone, id, revision) { phone.workout(id) }
        JListPage((read as? HealthRead.Ready)?.value?.let { activity(it.activity) } ?: title, onBack = back, onRefresh = retry) {
            when (read) {
                HealthRead.Loading -> item { JLoading() }
                is HealthRead.Failed -> item { HealthFailure(read.problem, retry, onAccess) }
                is HealthRead.Ready -> {
                    val row = read.value
                    item {
                        val locale = currentLocale
                        JSectionHeader(dateText(row.start, locale))
                        val stats = buildList {
                            add(JStat(stringResource(R.string.health_duration), stringResource(R.string.health_minutes, number(row.duration / 60, locale))))
                            row.distance?.let { add(JStat(stringResource(R.string.health_distance), stringResource(R.string.health_km, number(it / 1000, locale)))) }
                            row.energy?.let { add(JStat(stringResource(R.string.health_energy), stringResource(R.string.health_kcal, number(it, locale)))) }
                            row.averageHeartRate?.let { add(JStat(stringResource(R.string.health_avg_hr), stringResource(R.string.health_bpm, number(it, locale)))) }
                            row.maxHeartRate?.let { add(JStat(stringResource(R.string.health_max_hr), stringResource(R.string.health_bpm, number(it, locale)))) }
                            row.elevation?.let { add(JStat(stringResource(R.string.health_elevation), stringResource(R.string.health_m, number(it, locale)))) }
                        }
                        JStatGrid(stats)
                    }
                    if (row.heartRate.isNotEmpty()) item {
                        val locale = currentLocale
                        HealthPlot(stringResource(R.string.health_heartrate), row.heartRate.map { stringResource(R.string.health_minutes, number(it.minute.toDouble(), locale)) },
                            row.heartRate.map { it.bpm }, row.heartRate.map { stringResource(R.string.health_bpm, number(it.bpm, locale)) }, false)
                    }
                    if (row.splits.isNotEmpty()) item {
                        val locale = currentLocale
                        HealthPlot(stringResource(R.string.health_splits), row.splits.map { stringResource(R.string.health_km, number(it.km.toDouble(), locale)) },
                            row.splits.map { it.seconds / 60.0 }, row.splits.map { stringResource(R.string.health_minutes, number(it.seconds / 60.0, locale)) }, true)
                    }
                }
            }
        }
    }
}

@Composable
private fun HealthPlot(title: String, dates: List<String>, values: List<Double>, displays: List<String>, bar: Boolean) {
    if (values.size == 1) {
        JStatGrid(listOf(JStat(title, displays.single(), detail = dates.single())))
    } else {
        JChart(JChartData(if (bar) JChartKind.Bar else JChartKind.Line, dates, listOf(JChartSeries(title, values, displays))),
            Modifier.fillMaxWidth().padding(vertical = Tokens.Space.s), title = title)
    }
}

private fun number(value: Double, locale: Locale): String =
    NumberFormat.getNumberInstance(locale).apply { maximumFractionDigits = 1 }.format(value)

private fun dateText(value: String, locale: Locale): String = runCatching {
    val date = if (value.length == 10) LocalDate.parse(value) else OffsetDateTime.parse(value).toLocalDate()
    date.format(DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale))
}.getOrDefault(value)

@Composable
private fun activity(name: String): String = stringResource(when (name) {
    "Running" -> R.string.health_running
    "Walking" -> R.string.health_walking
    "Cycling" -> R.string.health_cycling
    "Swimming" -> R.string.health_swimming
    "Hiking" -> R.string.health_hiking
    "Yoga" -> R.string.health_yoga
    "Strength training" -> R.string.health_strength
    "Interval training" -> R.string.health_interval
    else -> R.string.health_workout
})
