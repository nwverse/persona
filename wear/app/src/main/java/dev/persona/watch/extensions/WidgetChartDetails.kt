package dev.persona.watch.extensions

/** Chart values are private projection content and cannot outlive their selected source. */
internal class WidgetChartDetails {
    var value: WidgetItem.Chart? = null
        private set
    private var owner: ExtensionWidget? = null

    fun open(widget: ExtensionWidget?, projection: CachedWidget?, chart: WidgetItem.Chart) {
        clear()
        if (widget != null && projection != null && projection.widget == widget && chart in projection.view.items) {
            owner = widget
            value = chart
        }
    }

    fun clear() { owner = null; value = null }

    fun reconcile(widget: ExtensionWidget?, projection: CachedWidget?) {
        if (widget == null || widget != owner || projection == null || projection.widget != widget ||
            projection.view.items.none { it == value }) clear()
    }
}
