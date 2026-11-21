package dev.persona.watch.extensions

import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubException
import dev.persona.watch.data.HubTransport
import dev.persona.watch.data.UrlConnectionTransport
import org.json.JSONObject
import java.net.URLEncoder
import java.time.ZoneId

/** Read-only widget endpoints only. A successful catalogue response discovers support without a forbidden WS hello. */
class ExtensionClient(
    config: HubConfig,
    locale: String = "en",
    timeZone: String = ZoneId.systemDefault().id,
    private val transport: HubTransport = UrlConnectionTransport(),
    private val now: () -> Long = System::currentTimeMillis,
) {
    val config = config.validated()
    private val locale = locale.takeIf { it.matches(Regex("[A-Za-z-]{2,35}")) } ?: "en"
    private val timeZone = runCatching { ZoneId.of(timeZone).id }.getOrDefault("UTC")
    private var catalog: List<ExtensionWidget>? = null
    private val cache = mutableMapOf<String, CachedWidget>()

    fun widgets(): List<ExtensionWidget> {
        val found = ExtensionParser.catalog(request("GET", "/api/v1/extensions/-/widgets"))
        catalog = found
        cache.keys.retainAll(found.flatMap { widget -> widget.families.map { "${widget.key}|$it" } }.toSet())
        return found
    }

    /** Only a widget from this client's current catalogue can be projected; never a full extension page. */
    fun widget(widget: ExtensionWidget, force: Boolean = false, family: String = widget.family): ExtensionWidgetView =
        projection(widget, force, family).view

    /** Preserve the original fetch time when a foreground refresh reuses a cached projection. */
    internal fun projection(widget: ExtensionWidget, force: Boolean = false, family: String = widget.family): CachedWidget {
        if (!canProject(widget)) throw ExtensionException(ExtensionProblem.UNAVAILABLE)
        if (family !in widget.families || family !in ExtensionParser.FAMILIES) throw ExtensionException(ExtensionProblem.UNAVAILABLE)
        val key = "${widget.key}|$family"
        if (!force) cache[key]?.takeIf { it.widget == widget && it.fresh(now()) }?.let { return it }
        val context = JSONObject().put("size", family).put("tz", timeZone).put("locale", locale)
            .put("platform", "android").put("ui_version", 3)
        val answer = try { ExtensionParser.view(request("POST", "/api/v1/extensions/${encode(widget.owner)}/-/widget/${encode(widget.id)}",
            JSONObject().put("context", context)), family) }
        catch (failure: ExtensionException) {
            if (failure.problem == ExtensionProblem.UNAVAILABLE) cache.remove(key)
            throw failure
        }
        return CachedWidget(widget, family, answer, now()).also { cache[key] = it }
    }

    /** A disk catalogue cannot grant this capability; only a successful discovery by this client can. */
    internal fun canProject(widget: ExtensionWidget): Boolean = catalog?.any { it == widget } == true

    private fun request(method: String, path: String, body: JSONObject? = null): JSONObject {
        val response = try { transport.request(config, method, path, body?.toString(), mapOf("Accept-Language" to locale)) }
        catch (_: HubException) { throw ExtensionException(ExtensionProblem.RETRYABLE) }
        catch (_: Exception) { throw ExtensionException(ExtensionProblem.RETRYABLE) }
        val problem = when (response.status) {
            in 200..299 -> null
            401 -> ExtensionProblem.AUTHENTICATION
            403 -> ExtensionProblem.FORBIDDEN
            404, 405, 410 -> ExtensionProblem.UNAVAILABLE
            408, 429, in 500..599 -> ExtensionProblem.RETRYABLE
            else -> ExtensionProblem.INVALID_RESPONSE
        }
        if (problem != null) {
            if (problem == ExtensionProblem.AUTHENTICATION || problem == ExtensionProblem.FORBIDDEN) { catalog = null; cache.clear() }
            throw ExtensionException(problem)
        }
        if (response.body.toByteArray(Charsets.UTF_8).size > MAX_RESPONSE_BYTES) throw ExtensionException(ExtensionProblem.INVALID_RESPONSE)
        return try { JSONObject(response.body) } catch (_: Exception) { throw ExtensionException(ExtensionProblem.INVALID_RESPONSE) }
    }

    companion object {
        private const val MAX_RESPONSE_BYTES = 1024 * 1024
        private fun encode(value: String) = URLEncoder.encode(value, "UTF-8").replace("+", "%20")
    }
}
