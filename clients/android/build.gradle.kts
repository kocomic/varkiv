buildscript {
    dependencies {
        // AGP owns Kotlin integration; pin the compiler without applying kotlin-android.
        classpath("org.jetbrains.kotlin:kotlin-gradle-plugin:2.4.10")
    }
}

plugins {
    id("com.android.application") version "9.4.0" apply false
}
