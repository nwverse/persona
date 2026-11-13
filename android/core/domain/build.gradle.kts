plugins {
    id("persona.jvm.library")
    // FixtureReader, for other modules' tests replaying the shared fixtures (core:data's chat sync scenarios).
    `java-test-fixtures`
}

dependencies {
    api(project(":core:contracts"))
    testImplementation(libs.coroutines.test)
}
