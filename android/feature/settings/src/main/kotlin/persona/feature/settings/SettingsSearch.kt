package persona.feature.settings

import android.content.res.Resources
import java.text.Normalizer

/** Entry names and aliases only: never searches account values, addresses or credentials. */
internal fun settingsSearchMatches(query: String, title: String, subtitle: String? = null, aliases: String = ""): Boolean {
    fun normalize(value: String) = Normalizer.normalize(value, Normalizer.Form.NFKC).lowercase(java.util.Locale.ROOT)
    val terms = normalize(query).trim().split(Regex("\\s+")).filter(String::isNotBlank)
    val text = normalize(listOfNotNull(title, subtitle, aliases).joinToString(" "))
    return terms.all(text::contains)
}
