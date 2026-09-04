import org.gradle.api.tasks.Sync

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val releaseKeystorePath = providers.environmentVariable("ANDROID_KEYSTORE_PATH").orNull
val releaseKeystorePassword = providers.environmentVariable("ANDROID_KEYSTORE_PASSWORD").orNull
val releaseKeyAlias = providers.environmentVariable("ANDROID_KEY_ALIAS").orNull
val releaseKeyPassword = providers.environmentVariable("ANDROID_KEY_PASSWORD").orNull
val appVersionName = rootProject.file("../../internal/buildinfo/VERSION").readText().trim()
val releaseSigningConfigured = listOf(
    releaseKeystorePath,
    releaseKeystorePassword,
    releaseKeyAlias,
    releaseKeyPassword,
).all { !it.isNullOrBlank() }

val thirdPartyAssetsDir = layout.buildDirectory.dir("generated/third-party-license-assets")
val generateThirdPartyLicenseAssets by tasks.registering(Sync::class) {
    from(rootProject.file("../../docs/THIRD_PARTY_NOTICES.md"))
    from(rootProject.file("../../docs/licenses/Apache-2.0.txt"))
    into(thirdPartyAssetsDir)
}

android {
    namespace = "org.varkiv.agent"
    compileSdk = 36

    buildFeatures {
        buildConfig = true
    }

    sourceSets {
        getByName("main").assets.srcDir(thirdPartyAssetsDir)
    }

    defaultConfig {
        applicationId = "org.varkiv.agent"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = appVersionName
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    signingConfigs {
        if (releaseSigningConfigured) {
            create("release") {
                storeFile = file(requireNotNull(releaseKeystorePath))
                storePassword = releaseKeystorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.findByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}

tasks.named("preBuild").configure {
    dependsOn(generateThirdPartyLicenseAssets)
}

dependencies {
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test:core:1.7.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
}
