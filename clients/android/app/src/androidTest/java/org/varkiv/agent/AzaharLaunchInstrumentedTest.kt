package org.varkiv.agent

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class AzaharLaunchInstrumentedTest {
    @Test
    fun launchesPinnedAzaharVariantWithSafContentUri() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val arguments = InstrumentationRegistry.getArguments()
        val fixtureName = arguments.getString("e2e_azahar_3dsx_file").orEmpty()
        val expectedPackage = arguments.getString("e2e_azahar_package").orEmpty()
        assumeTrue("Real Azahar launch requires the isolated acceptance script", fixtureName.isNotBlank())
        require(Regex("^[a-z0-9][a-z0-9.-]{0,63}\\.3dsx$").matches(fixtureName)) { "Unsafe 3DS fixture name" }
        require(expectedPackage == "org.azahar_emu.azahar" || expectedPackage == "io.github.lime3ds.android") {
            "Unexpected Azahar package"
        }
        val context = instrumentation.targetContext
        val fixtureFile = java.io.File(context.filesDir, fixtureName)
        require(fixtureFile.isFile && fixtureFile.length() in 1..(2L * 1024L * 1024L)) { "3DS fixture is unavailable or oversized" }
        val fixtureBytes = fixtureFile.readBytes()
        val relative = "roms/varkiv-fixture.3dsx"
        TestDocumentsProvider.reset(context, mapOf(relative to fixtureBytes))
        val romUri = TestDocumentsProvider.documentUri(relative)
        context.contentResolver.openInputStream(romUri).use { input ->
            assertArrayEquals(fixtureBytes, requireNotNull(input).readBytes())
        }
        val baselineOpenCount = TestDocumentsProvider.openCount(context, relative)

        val launch = JSONObject()
            .put("edition_id", "android-real-azahar-e2e")
            .put("platform_id", "3ds")
            .put("rom_uri", romUri.toString())
            .put(
                "intent",
                JSONObject()
                    .put("action", "android.intent.action.VIEW")
                    .put("package", "org.azahar_emu.azahar")
                    .put("package_candidates", JSONArray().put("io.github.lime3ds.android"))
                    .put("activity", "org.citra.citra_emu.activities.EmulationActivity")
                    .put("data", "{{rom.uri}}")
                    .put("mime_type", "application/octet-stream")
                    .put("categories", JSONArray().put("android.intent.category.DEFAULT"))
                    .put("flags", JSONArray().put("grant-read-uri").put("new-task").put("clear-top")),
            )

        val prepared = IntentLauncher.prepare(context, launch)
        assertEquals(expectedPackage, prepared.packageName)
        assertEquals(romUri, prepared.intent.data)
        assertEquals("application/octet-stream", prepared.intent.type)
        assertTrue(prepared.intent.clipData?.getItemAt(0)?.uri == romUri)
        assertTrue(prepared.uriGrantFlags and android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION != 0)

        IntentLauncher.launch(context, launch)
        val deadline = System.currentTimeMillis() + 15_000
        while (TestDocumentsProvider.openCount(context, relative) <= baselineOpenCount && System.currentTimeMillis() < deadline) {
            Thread.sleep(100)
        }
        assertTrue(
            "Pinned Azahar did not open the granted SAF homebrew",
            TestDocumentsProvider.openCount(context, relative) > baselineOpenCount,
        )

        Thread.sleep(12_000)
        val screenshot = requireNotNull(instrumentation.uiAutomation.takeScreenshot())
        val output = java.io.File(context.filesDir, "azahar-real-launch.png")
        output.outputStream().use { stream ->
            check(screenshot.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, stream))
        }
        val sampleStep = maxOf(1, minOf(screenshot.width, screenshot.height) / 160)
        var bright = 0
        var dark = 0
        val colors = mutableSetOf<Int>()
        for (y in 0 until screenshot.height step sampleStep) for (x in 0 until screenshot.width step sampleStep) {
            val color = screenshot.getPixel(x, y)
            val sum = android.graphics.Color.red(color) + android.graphics.Color.green(color) + android.graphics.Color.blue(color)
            if (sum > 420) bright++
            if (sum < 45) dark++
            colors.add(color and 0x00f8f8f8)
        }
        assertTrue("Azahar did not render the 3DS fixture text", bright in 13..499)
        assertTrue("A system or setup surface covered the 3DS frame", dark > 5_000)
        assertTrue("Azahar stayed on a flat system surface", colors.size > 8)
    }
}
