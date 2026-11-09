# Release shrinking and optimizing (docs/android/android.md "Release shrinking"): R8 removes what nothing reaches and
# optimizes the rest (inlining, class merging, dead branches), but obfuscates nothing, so error reports keep the
# source's class names (in_app is persona.*), simple names and source file names. Method names and lines in frames of
# optimized code can differ from the source's (inlined, merged or outlined methods, $r8$lambda$<hash> lambda bodies):
# the release worker keeps build/outputs/mapping/release/mapping.txt with each build, and retrace maps them back.
# Code reached only by name needs a keep rule here and a check in scripts/release/check_android_shrink.py.
-dontobfuscate
-keepattributes SourceFile,LineNumberTable

# Error reports and breadcrumbs print simple class names (ErrorReports.describe: javaClass.simpleName), which come
# from these attributes; without them a nested exception or route would report as Outer$Inner.
-keepattributes InnerClasses,EnclosingMethod

# Class merging folds an exception class into another (CoreTextException, ChannelRejected, ChannelReadOnly,
# ChannelsUnsupported and ModelPurposeNotFound in one build), and a report would then name the class it went into.
# Names only: R8 still removes the ones nothing throws. check_android_shrink.py fails on a persona Throwable that is
# neither in the dex nor listed as removed in usage.txt.
-keepnames class persona.** extends java.lang.Throwable

# An optimizing R8 keeps a nested class's InnerClass annotation only when it keeps the names of both the nested class
# (the rule above) and its outer class, so these nested exceptions would lose their simple names (Failure, WindowGap,
# ...) with either rule missing: keep the outer classes' names. The shrink check reports a new one ("InnerClass
# annotation removed") before signing.
-keepnames class persona.core.data.RealInboxRepository
-keepnames class persona.core.designsystem.MarkdownRenderer
-keepnames class persona.feature.agents.AgentsViewModel
-keepnames class persona.feature.files.Notebooks
-keepnames class persona.feature.inbox.LarkInboxRepository

# Looked up by name on a failed tasks call (TasksStore.reloadTodos and isLegacySpacesRoute in core/domain).
-keepclassmembers class persona.core.hub.HubFailure {
    public java.lang.Integer getStatus();
    public java.lang.String getCode();
}
