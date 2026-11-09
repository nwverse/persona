# Release shrinking only (docs/android/android.md "Release shrinking"): R8 removes what nothing reaches, but renames,
# optimizes and merges nothing, so error reports keep the source's class and method names (in_app is persona.*) and
# simple names. Lines stay the source's, except inside bodies kotlinc inlined from an inline function, which R8
# numbers by the inlined function; build/outputs/mapping/release/mapping.txt resolves those frames.
# Code reached only by name needs a keep rule here and a check in scripts/release/check_android_shrink.py.
-dontobfuscate
-dontoptimize
-keepattributes SourceFile,LineNumberTable

# Looked up by name on a failed tasks call (TasksStore.reloadTodos and isLegacySpacesRoute in core/domain).
-keepclassmembers class persona.core.hub.HubFailure {
    public java.lang.Integer getStatus();
    public java.lang.String getCode();
}
