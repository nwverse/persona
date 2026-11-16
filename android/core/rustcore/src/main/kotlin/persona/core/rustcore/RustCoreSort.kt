package persona.core.rustcore

/** Pure sort projection. Only the DEBUG sort shadow calls this wrapper; loading the class loads the native library. */
object RustCoreSort {
    fun project(input: String): String = NativeBridge.projectSort(input)
}
