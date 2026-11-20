package persona.feature.knowledge

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.toggleableState
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.*
import persona.core.contracts.parseTime
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import java.net.IDN
import java.net.URI

private val projectFields = listOf("summary", "one_line")

private const val fullMode = "full"
private const val referenceMode = "reference-only"

/** An older hub and an empty unmarked project have the same representation. */
internal val KnowledgeDetail.editableProjectMode: String?
    get() = when (projectMode) {
        fullMode, referenceMode -> projectMode
        null -> fullMode.takeIf { freshness != null || sources.isNotEmpty() || meta?.containsKey("source_urls") == true }
        else -> null
    }

@Composable
internal fun ProjectCoverageNotice(entry: KnowledgeEntry) {
    if (!entry.isProject) return
    if (entry.projectMode == referenceMode) {
        Text(stringResource(R.string.knowledge_evidence_reference_only), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
        return
    }
    val freshness = entry.freshness ?: return
    val lines = projectFields.mapNotNull { key ->
        val field = freshness.fields[key] ?: return@mapNotNull null
        val summary = key == "summary"
        when {
            field.needsReview -> {
                val label = stringResource(if (summary) R.string.knowledge_evidence_summary_review else R.string.knowledge_evidence_overview_review)
                val count = field.uncoveredEvidenceCount
                if (count != null && count > 0) pluralStringResource(R.plurals.knowledge_evidence_newer, count, label, count) else label
            }
            field.cutoff == null -> stringResource(if (summary) R.string.knowledge_evidence_summary_unknown else R.string.knowledge_evidence_overview_unknown)
            else -> null
        }
    }
    if (lines.isNotEmpty() || freshness.needsReview) {
        Text(if (lines.isEmpty()) stringResource(R.string.knowledge_evidence_review) else lines.joinToString("\n"),
            style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
    }
}

internal fun LazyListScope.projectEvidence(
    detail: KnowledgeDetail, open: Boolean, onToggle: () -> Unit, onEdit: () -> Unit,
    onEntry: (KnowledgeEntry) -> Unit, onWeb: (String) -> Unit,
) {
    if (detail.kind != KnowledgeKind.Projects.wire) return
    val mode = detail.editableProjectMode
    val freshness = detail.freshness
    if (mode == null && freshness == null && detail.sources.isEmpty()) return
    item("project-evidence") {
        JGroupItem(GroupPosition.Single) {
            JRow(stringResource(R.string.knowledge_evidence_title), Modifier.foldToggle(open, onToggle), titleLines = Int.MAX_VALUE,
                subtitle = if (mode == referenceMode) stringResource(R.string.knowledge_evidence_reference_only) else null,
                trailing = { JIcon(if (open) PersonaIcons.collapse else PersonaIcons.expand, null, decorative = true) })
        }
    }
    if (!open) return
    if (freshness != null) {
        val fields = projectFields.filter { it in freshness.fields }
        itemsIndexed(fields, key = { _, key -> "project-coverage-$key" }) { index, key ->
            val field = freshness.fields.getValue(key)
            JGroupItem(GroupPosition.of(index, fields.size)) {
                JRow(stringResource(if (key == "summary") R.string.knowledge_evidence_summary else R.string.knowledge_evidence_overview),
                    subtitle = fieldDescription(field, freshness.timelineEnd), titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE)
            }
        }
        if (freshness.evidence.isNotEmpty() || fields.any { freshness.fields[it]?.cutoff?.sources?.isNotEmpty() == true }) {
            item("project-source-revisions") { ProjectRevisions(detail.id, freshness) }
        }
    }
    if (detail.sources.isNotEmpty()) {
        item("project-sources") { JSectionHeader(stringResource(R.string.knowledge_evidence_sources)) }
        itemsIndexed(detail.sources, key = { _, source -> "project-source-${source.id}" }) { index, source ->
            JGroupItem(GroupPosition.of(index, detail.sources.size)) { ProjectSourceRows(source, detail.links.orEmpty(), onEntry, onWeb) }
        }
    }
    if (mode != null) item("project-storage") {
        JGroupItem(GroupPosition.Single) {
            val referenceOnly = mode == referenceMode
            JRow(stringResource(if (referenceOnly) R.string.knowledge_evidence_reference_only else R.string.knowledge_evidence_managed),
                subtitle = stringResource(if (referenceOnly) R.string.knowledge_evidence_reference_hint else R.string.knowledge_evidence_managed_hint),
                titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE)
            JRow(stringResource(R.string.knowledge_evidence_edit), leadingIcon = PersonaIcons.edit, onClick = onEdit)
        }
    }
}

@Composable
private fun ProjectRevisions(id: String, freshness: ProjectFreshness) {
    var open by rememberSaveable(id) { mutableStateOf(false) }
    JGroupItem(GroupPosition.Single) {
        JRow(stringResource(R.string.knowledge_evidence_revisions), Modifier.foldToggle(open) { open = !open },
            titleLines = Int.MAX_VALUE, trailing = { JIcon(if (open) PersonaIcons.collapse else PersonaIcons.expand, null, decorative = true) })
        if (open) {
            for (key in projectFields) {
                val recorded = freshness.fields[key]?.cutoff?.sources.orEmpty()
                if (recorded.isNotEmpty()) RevisionRow(stringResource(if (key == "summary") R.string.knowledge_evidence_summary else R.string.knowledge_evidence_overview), recorded)
            }
            if (freshness.evidence.isNotEmpty()) RevisionRow(stringResource(R.string.knowledge_evidence_current_sources), freshness.evidence)
        }
    }
}

@Composable
private fun RevisionRow(label: String, revisions: List<ProjectEvidenceRef>) {
    SelectionContainer {
        JRow(label, subtitle = revisions.joinToString("\n") { it.description }, titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE)
    }
}

private val ProjectEvidenceRef.description: String
    get() = listOfNotNull(ref, sourceId?.takeIf { it != ref }, position).joinToString(" · ")

@Composable
private fun fieldDescription(field: ProjectFieldFreshness, timelineEnd: Long): String = buildList {
    val cutoff = field.cutoff
    if (cutoff == null) {
        add(stringResource(R.string.knowledge_evidence_cutoff_unknown))
    } else {
        add(stringResource(R.string.knowledge_evidence_timeline, cutoff.timelineFrom, cutoff.timelineThrough, timelineEnd))
        if (cutoff.timelineTruncated) add(stringResource(R.string.knowledge_evidence_truncated))
    }
    field.reviewReason?.let { reason ->
        val label = when (reason) {
            "summary_edited" -> stringResource(R.string.knowledge_evidence_summary_changed)
            "evidence_changed" -> stringResource(R.string.knowledge_evidence_changed)
            "new_evidence" -> stringResource(R.string.knowledge_evidence_arrived)
            "coverage_unknown" -> if (cutoff == null) null else stringResource(R.string.knowledge_evidence_cutoff_unknown)
            referenceMode -> stringResource(R.string.knowledge_evidence_reference_only)
            else -> reason
        }
        label?.let { add(it) }
    }
    field.uncoveredEvidenceCount?.let { add(pluralStringResource(R.plurals.knowledge_evidence_uncovered, it, it)) }
    field.lockReason?.let { add(if (it == "field_locked") stringResource(R.string.knowledge_evidence_locked) else it) }
    add(attribution(field.writer, maintainer = false))
    add(attribution(field.reviewBy, maintainer = true))
    field.source?.let { add(stringResource(R.string.knowledge_evidence_provenance, it)) }
    if (cutoff != null && cutoff.sources.isNotEmpty()) {
        add(stringResource(R.string.knowledge_evidence_based_on, cutoff.sources.joinToString("\n") { it.ref }))
    }
}.joinToString("\n")

@Composable
private fun ProjectSourceRows(source: ProjectSource, links: List<KnowledgeEntry>, onEntry: (KnowledgeEntry) -> Unit, onWeb: (String) -> Unit) {
    val url = source.url?.takeIf(::isWebRef)
    val entries = source.refs.mapNotNull { refEntry(it, links) }
    val first = if (url == null) entries.firstOrNull() else null
    val title = url ?: first?.name ?: source.id
    val locale = currentLocale
    val time = remember(source.deliveryAt, locale) {
        source.deliveryAt?.let(::parseTime)?.let { LocalizedFormats.format(it, LocalizedFormats.DATE_TIME, locale) }
    }
    val info = buildList {
        if (source.refs.isNotEmpty()) add(source.refs.joinToString("\n"))
        source.deliveryState?.takeIf(String::isNotBlank)?.let { state ->
            val name = when (state) {
                "open" -> stringResource(R.string.knowledge_evidence_open)
                "closed" -> stringResource(R.string.knowledge_evidence_closed)
                "merged" -> stringResource(R.string.knowledge_evidence_merged)
                else -> state
            }
            add(stringResource(R.string.knowledge_evidence_source_status, name))
        }
        if (time != null) add(time)
        val verification = source.environmentVerification
        if (verification == "unverified" || (verification == null && !source.deliveryState.isNullOrBlank())) {
            add(stringResource(R.string.knowledge_evidence_verification))
        } else if (!verification.isNullOrBlank()) {
            add(stringResource(R.string.knowledge_evidence_environment_state, verification))
        }
    }.joinToString("\n").ifEmpty { null }
    val action: (() -> Unit)? = when {
        url != null -> { { onWeb(url) } }
        first != null -> { { onEntry(first) } }
        else -> null
    }
    if (action != null) JRow(title, subtitle = info, onClick = action, titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE)
    else SelectionContainer { JRow(title, subtitle = info, titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE) }
    for (entry in entries) if (entry != first) {
        JRow(entry.name, onClick = { onEntry(entry) }, titleLines = Int.MAX_VALUE)
    }
}

@Composable
private fun attribution(who: String?, maintainer: Boolean): String = when {
    who == "user" -> stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_user else R.string.knowledge_evidence_writer_user)
    who == "persona" -> stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_persona else R.string.knowledge_evidence_writer_persona)
    who?.startsWith("ext:") == true && who.length > 4 ->
        stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_extension else R.string.knowledge_evidence_writer_extension, who.substring(4))
    else -> stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_unknown else R.string.knowledge_evidence_writer_unknown)
}

/** A field path such as projects/orchard/log identifies evidence without opening another project. */
private fun refEntry(ref: String, links: List<KnowledgeEntry>): KnowledgeEntry? {
    val parts = ref.split('/', limit = 3)
    if (parts.size != 2 || parts[1].isBlank() || parts[1] in setOf(".", "..")) return null
    val kind = KnowledgeKind.fromWire(parts[0]) ?: return null
    return links.firstOrNull { it.kind == kind.wire && it.id == parts[1] } ?: KnowledgeEntry(kind.wire, parts[1], ref)
}

private fun isWebRef(ref: String): Boolean = runCatching {
    val uri = URI(ref)
    if (uri.scheme?.lowercase() !in setOf("http", "https") || uri.rawAuthority == null || '@' in uri.rawAuthority) return false
    // URI.host only recognizes ASCII names. Both the browser and the hub accept internationalized hosts.
    !uri.host.isNullOrBlank() || IDN.toASCII(uri.toURL().host, IDN.USE_STD3_ASCII_RULES).isNotBlank()
}.getOrDefault(false)

@Composable
internal fun ProjectStorageEditor(detail: KnowledgeDetail, work: KnowledgeTagging, onSave: (KnowledgeUpdate) -> Unit, onDismiss: () -> Unit) {
    val mode = detail.editableProjectMode ?: return
    val originalPolicy = rememberSaveable { mode }
    // The top-level source catalog includes automatic relationships. Only owner-entered URLs are editable.
    val originalRefs = rememberSaveable { detail.meta.strings("source_urls").joinToString("\n") }
    var policy by rememberSaveable { mutableStateOf(originalPolicy) }
    var text by rememberSaveable { mutableStateOf(originalRefs) }
    val changedRefs = if (text == originalRefs) null else text.lineSequence().map(String::trim).filter(String::isNotEmpty)
        .toList().takeUnless { it.joinToString("\n") == originalRefs }
    val validRefs = changedRefs == null || changedRefs.size <= 30 && changedRefs.all { isWebRef(it) && it.toByteArray().size <= 2048 }
    val update = KnowledgeUpdate(projectMode = policy.takeIf { it != originalPolicy }, sourceUrls = changedRefs)
    JSheet(onDismiss, title = stringResource(R.string.knowledge_evidence_edit), dismissible = !work.busy, scrollable = true) {
        Column(Modifier.fillMaxWidth().imePadding(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            JSwitchRow(stringResource(R.string.knowledge_evidence_reference_only), policy == referenceMode, { policy = if (it) referenceMode else fullMode },
                Modifier.testTag("project-storage-policy").semantics { toggleableState = ToggleableState(policy == referenceMode) },
                subtitle = stringResource(R.string.knowledge_evidence_reference_hint), subtitleLines = Int.MAX_VALUE, enabled = !work.busy)
            JTextField(text, { text = it }, Modifier.fillMaxWidth().testTag("project-source-refs"), label = stringResource(R.string.knowledge_evidence_refs),
                supportingText = stringResource(if (validRefs) R.string.knowledge_evidence_refs_hint else R.string.knowledge_evidence_invalid_refs), minLines = 3, enabled = !work.busy, isError = !validRefs)
            work.error?.let { JNotice(stringResource(R.string.knowledge_not_saved, it.text()), status = Status.NeedsYou) }
            JButton(stringResource(R.string.knowledge_save), { onSave(update) }, Modifier.fillMaxWidth(),
                enabled = validRefs && (update.projectMode != null || update.sourceUrls != null), loading = work.busy, loadingText = stringResource(R.string.knowledge_saving))
            JButton(stringResource(R.string.knowledge_evidence_cancel), onDismiss, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, enabled = !work.busy)
        }
    }
}
