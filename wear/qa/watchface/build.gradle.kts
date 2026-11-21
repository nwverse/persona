plugins { id("com.android.application") version "8.13.2" }

android {
    namespace = "dev.persona.watch.qa.face"
    compileSdk = 35
    defaultConfig {
        applicationId = "dev.persona.watch.qa.face"
        minSdk = 34
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"
    }
    lint { abortOnError = true }
}
