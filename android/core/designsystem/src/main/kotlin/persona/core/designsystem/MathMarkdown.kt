package persona.core.designsystem

import java.net.URLDecoder
import java.net.URLEncoder

/**
 * Formulas in Markdown (docs/android/android.md "Files and Git in chat" › "Formulas and diagrams", iOS `MathMarkdown.swift`), rewritten into what the Markdown
 * renderer can carry before it parses:
 * - display math — `$$…$$` and `\[…\]` on lines of their own, or a line that is one formula — becomes a ```math fence
 *   (drawn by [RenderedFence]);
 * - inline math — `$…$` and `\(…\)` — becomes an inline image `![…](persona-math:…)` ([MathImages]).
 * Code spans, fences and indented code are left alone. A `$` opens only before a non-space and closes only after one
 * and not before a digit (pandoc's rule), so "$5 and $10" stays text; `\$` is a dollar. A span with CJK words in it is
 * prose, not a formula.
 */
object MathMarkdown {
    const val SCHEME = "persona-math"
    const val FENCE = "math"

    fun rewrite(md: String): String {
        if (!md.contains('$') && !md.contains("\\(") && !md.contains("\\[")) return md
        val out = ArrayList<String>()
        var fence: String? = null
        var blockEnd: String? = null
        val blockLines = ArrayList<String>()
        for (line in md.split('\n')) {
            val t = line.trim(' ', '\t')
            if (blockEnd != null) {
                if (t == blockEnd) {
                    out += "```$FENCE"; out += blockLines; out += "```"
                    blockEnd = null; blockLines.clear()
                } else blockLines += line
                continue
            }
            if (fence != null) {
                if (t.startsWith(fence)) fence = null
                out += line
                continue
            }
            if (t.startsWith("```") || t.startsWith("~~~")) {
                fence = t.take(3)
                out += line
                continue
            }
            if (line.startsWith("    ") || line.startsWith("\t")) {
                out += line
                continue
            }
            if (t == "$$" || t == "\\[") {
                blockEnd = if (t == "$$") "$$" else "\\]"
                continue
            }
            val tex = display(t)
            if (tex != null) {
                out += "```$FENCE"; out += tex; out += "```"
                continue
            }
            out += inline(line)
        }
        blockEnd?.let { end -> // never closed: as it was
            out += if (end == "$$") "$$" else "\\["
            out += blockLines
        }
        return out.joinToString("\n")
    }

    /** A line that is one formula: `$$x$$`, `\[x\]`, or `$x$` alone. */
    fun display(t: String): String? {
        for ((open, close) in listOf("$$" to "$$", "\\[" to "\\]")) {
            if (t.length > open.length + close.length && t.startsWith(open) && t.endsWith(close)) {
                val tex = t.substring(open.length, t.length - close.length)
                if (!tex.contains(close) && tex.isNotBlank()) return tex.trim(' ', '\t')
            }
        }
        val spans = inlineSpans(t)
        if (spans.size == 1 && spans[0].start == 0 && spans[0].end == t.length) return spans[0].tex
        return null
    }

    /** A formula in a line: [start, end) and its TeX. */
    data class Span(val start: Int, val end: Int, val tex: String)

    /** The inline formulas of a line, outside code spans. */
    fun inlineSpans(c: String): List<Span> {
        val spans = ArrayList<Span>()
        var i = 0
        var tick = 0 // inside a code span of this many backticks
        while (i < c.length) {
            if (c[i] == '`') {
                var n = 0
                while (i + n < c.length && c[i + n] == '`') n++
                tick = if (tick == 0) n else if (tick == n) 0 else tick
                i += n
                continue
            }
            if (tick > 0) { i++; continue }
            if (c[i] == '\\' && i + 1 < c.length) {
                val end = if (c[i + 1] == '(') c.indexOf("\\)", i + 2).takeIf { it >= 0 } else null
                if (end != null) {
                    spans += Span(i, end + 2, c.substring(i + 2, end))
                    i = end + 2
                } else i += 2 // an escape (\$ is a dollar)
                continue
            }
            if (c[i] == '$' && i + 1 < c.length && c[i + 1] != '$' && !c[i + 1].isWhitespace()) {
                var j = i + 1
                var close: Int? = null
                while (j < c.length) {
                    if (c[j] == '\\') { j += 2; continue }
                    if (c[j] == '$') {
                        val afterDigit = j + 1 < c.length && c[j + 1].isDigit()
                        if (!c[j - 1].isWhitespace() && !afterDigit && (j + 1 >= c.length || c[j + 1] != '$')) close = j
                        break
                    }
                    j++
                }
                if (close != null && plausible(c.substring(i + 1, close))) {
                    spans += Span(i, close + 1, c.substring(i + 1, close))
                    i = close + 1
                    continue
                }
            }
            i++
        }
        return spans
    }

    /**
     * A formula has no Chinese or Japanese words in it (outside `\text{…}`): between "$10 a month" written in Chinese and the next `$` is
     * prose with two dollars in it.
     */
    private fun plausible(tex: String): Boolean {
        if (tex.length > 400) return false
        val wide = tex.any { it.code in 0x3000..0x9FFF || it.code in 0xFF00..0xFFEF }
        return !wide || tex.contains("\\text")
    }

    private fun inline(line: String): String {
        val spans = inlineSpans(line)
        if (spans.isEmpty()) return line
        val out = StringBuilder()
        var at = 0
        for (s in spans) {
            out.append(line, at, s.start).append("![").append(label(s.tex)).append("](").append(url(s.tex)).append(")")
            at = s.end
        }
        return out.append(line.substring(at)).toString()
    }

    /** The image's alt text (what TalkBack reads): the TeX, without what would end the brackets. */
    private fun label(tex: String) = tex.replace('[', '(').replace(']', ')').replace('\n', ' ')

    fun url(tex: String): String = "$SCHEME:" + URLEncoder.encode(tex, "UTF-8").replace("+", "%20")

    /** The TeX of a `persona-math:` link, or null for any other link. */
    fun tex(link: String): String? =
        if (link.startsWith("$SCHEME:")) runCatching { URLDecoder.decode(link.substring(SCHEME.length + 1).replace("+", "%2B"), "UTF-8") }.getOrNull()
        else null
}
