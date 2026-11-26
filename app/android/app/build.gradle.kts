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
        versionCode = 2
        versionName = "0.1.1"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    val signingStore = System.getenv("ANDROID_KEYSTORE_PATH")
    if (!signingStore.isNullOrBlank()) {
        signingConfigs {
            create("release") {
                storeFile = file(signingStore)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            isMinifyEnabled = false
            signingConfigs.findByName("release")?.let { signingConfig = it }
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
