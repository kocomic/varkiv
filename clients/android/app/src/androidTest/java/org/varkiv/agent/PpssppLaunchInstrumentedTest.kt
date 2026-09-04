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
class PpssppLaunchInstrumentedTest {
    @Test
    fun launchesPinnedPpssppWithSafContentUri() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val arguments = InstrumentationRegistry.getArguments()
        val fixtureName = arguments.getString("e2e_ppsspp_pbp_file").orEmpty()
        assumeTrue("Real PPSSPP launch requires the isolated acceptance script", fixtureName.isNotBlank())
        require(Regex("^[a-z0-9][a-z0-9.-]{0,63}\\.pbp$").matches(fixtureName)) { "Unsafe PSP fixture name" }
        val context = instrumentation.targetContext
        val fixtureFile = java.io.File(context.filesDir, fixtureName)
        require(fixtureFile.isFile && fixtureFile.length() in 1..(1024L * 1024L)) { "PSP fixture is unavailable or oversized" }
        val pbpBytes = fixtureFile.readBytes()
        val relative = "roms/varkiv-fixture.pbp"
        TestDocumentsProvider.reset(context, mapOf(relative to pbpBytes))
        val romUri = TestDocumentsProvider.documentUri(relative)
        context.contentResolver.openInputStream(romUri).use { input ->
            assertArrayEquals(pbpBytes, requireNotNull(input).readBytes())
        }
        val baselineOpenCount = TestDocumentsProvider.openCount(context, relative)

        val launch = JSONObject()
            .put("edition_id", "android-real-ppsspp-e2e")
            .put("platform_id", "psp")
            .put("rom_uri", romUri.toString())
            .put(
                "intent",
                JSONObject()
                    .put("action", "android.intent.action.VIEW")
                    .put("package", "org.ppsspp.ppsspp")
                    .put("activity", ".PpssppActivity")
                    .put("data", "{{rom.uri}}")
                    .put("mime_type", "application/octet-stream")
                    .put("categories", JSONArray().put("android.intent.category.DEFAULT"))
                    .put("flags", JSONArray().put("grant-read-uri").put("new-task").put("clear-top")),
            )

        val prepared = IntentLauncher.prepare(context, launch)
        assertEquals("The PPSSPP SAF data URI drifted when MIME was applied", romUri, prepared.intent.data)
        assertEquals("application/octet-stream", prepared.intent.type)
        assertTrue(prepared.intent.clipData?.getItemAt(0)?.uri == romUri)
        assertTrue(prepared.uriGrantFlags and android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION != 0)

        IntentLauncher.launch(context, launch)
        val deadline = System.currentTimeMillis() + 10_000
        while (TestDocumentsProvider.openCount(context, relative) <= baselineOpenCount && System.currentTimeMillis() < deadline) {
            Thread.sleep(100)
        }
        assertTrue(
            "Pinned PPSSPP did not open the granted SAF homebrew",
            TestDocumentsProvider.openCount(context, relative) > baselineOpenCount,
        )

        Thread.sleep(12_000)
        val screenshot = requireNotNull(instrumentation.uiAutomation.takeScreenshot())
        val output = java.io.File(context.filesDir, "ppsspp-real-launch.png")
        output.outputStream().use { stream ->
            check(screenshot.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, stream))
        }
        val sampleStep = maxOf(1, minOf(screenshot.width, screenshot.height) / 160)
        var nonBlack = 0
        var upperBright = 0
        val colors = mutableSetOf<Int>()
        for (y in 0 until screenshot.height step sampleStep) for (x in 0 until screenshot.width step sampleStep) {
            val color = screenshot.getPixel(x, y)
            val red = android.graphics.Color.red(color)
            val green = android.graphics.Color.green(color)
            val blue = android.graphics.Color.blue(color)
            if (red + green + blue > 60) nonBlack++
            if (y < screenshot.height / 2 && red + green + blue > 420) upperBright++
            colors.add(color and 0x00f8f8f8)
        }
        val corner = screenshot.getPixel(screenshot.width / 20, screenshot.height / 20)
        val cornerBrightness = android.graphics.Color.red(corner) + android.graphics.Color.green(corner) + android.graphics.Color.blue(corner)
        assertTrue("A system first-run surface covered the PSP frame", cornerBrightness < 360)
        assertTrue("PPSSPP stayed on a black surface", nonBlack > 80)
        assertTrue("The Varkiv PSP fixture text was not visible", upperBright > 10)
        assertTrue("PPSSPP did not render the PSP fixture", colors.size > 4)
    }
}
