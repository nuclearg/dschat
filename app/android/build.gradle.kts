// AGP 9.5 ships built-in Kotlin support, so no separate Kotlin Gradle plugin is
// applied: `com.android.application` compiles src/main/java/*.kt itself.
// AGP 9.5.1 requires Gradle >= 9.6.0 and supports compileSdk 37.
plugins {
    id("com.android.application") version "9.5.1" apply false
}
