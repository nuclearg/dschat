plugins {
    id("com.android.application")
}

android {
    namespace = "com.dschat.android"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.dschat.android"
        minSdk = 26
        targetSdk = 37
        versionCode = 4
        versionName = "0.1.3"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            isMinifyEnabled = false
        }
    }

    // The gomobile AAR is a plain file dependency: no remote repository needed.
    packaging {
        jniLibs {
            useLegacyPackaging = false
        }
    }
}

dependencies {
    implementation(files("libs/chatsvr.aar"))
}
