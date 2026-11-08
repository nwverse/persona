plugins {
    id("persona.android.library")
    id("persona.hilt")
}

android {
    namespace = "persona.platform.notify"
    defaultConfig.testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    sourceSets.getByName("test").kotlin.srcDir("src/sharedTest/kotlin")
    sourceSets.getByName("androidTest").kotlin.srcDir("src/sharedTest/kotlin")
    testOptions.unitTests.isIncludeAndroidResources = true
    testOptions.unitTests.all {
        it.systemProperty("persona.repo.root", rootProject.projectDir.parentFile.absolutePath)
        // One JVM per test class (#1254). WorkManager's startup initializer runs its first database work on a
        // background thread that outlives the test, and that work loads Robolectric's native runtime. When the next
        // class uses a different sandbox (ForegroundRecoveryRegressionTest is SDK 34 / legacy graphics, the rest
        // SDK 35), both sandboxes open the same runtime jar through the JVM-wide zip file system registry at once and
        // one fails with FileSystemAlreadyExistsException. A fresh JVM per class keeps that leftover work and the
        // registry from reaching the next class.
        it.forkEvery = 1
    }
}

dependencies {
    implementation(project(":core:contracts"))
    implementation(project(":core:domain"))
    implementation(project(":core:designsystem"))
    implementation(platform(libs.compose.bom))
    implementation(project(":core:navigation"))
    implementation(libs.androidx.core)
    implementation(libs.androidx.work)
    implementation(libs.coroutines.android)
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    testImplementation(libs.junit)
    testImplementation(libs.coroutines.test)
    testImplementation(libs.serialization.json)
    testImplementation("androidx.work:work-testing:${libs.versions.work.get()}")
    testImplementation("org.robolectric:robolectric:4.17")
    testImplementation("androidx.test.ext:junit:1.3.0")
}
