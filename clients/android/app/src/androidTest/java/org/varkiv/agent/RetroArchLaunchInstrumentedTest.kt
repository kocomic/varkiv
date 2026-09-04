package org.varkiv.agent

import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class RetroArchLaunchInstrumentedTest {
    @Test
    fun launchesPinnedRetroArchWithSafContentUri() {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val arguments = InstrumentationRegistry.getArguments()
        val encodedROM = arguments.getString("e2e_retroarch_rom_base64").orEmpty()
        assumeTrue("Real RetroArch launch requires the isolated acceptance script", encodedROM.isNotBlank())
        val context = instrumentation.targetContext
        val romBytes = Base64.decode(encodedROM, Base64.NO_WRAP)
        val relative = "roms/hello.gba"
        TestDocumentsProvider.reset(context, mapOf(relative to romBytes))
        val romUri = TestDocumentsProvider.documentUri(relative)
        context.contentResolver.openInputStream(romUri).use { input ->
            assertArrayEquals(romBytes, requireNotNull(input).readBytes())
        }
        val baselineOpenCount = TestDocumentsProvider.openCount(context, relative)

        val launch = JSONObject()
            .put("edition_id", "android-real-retroarch-e2e")
            .put("platform_id", "gba")
            .put("core_library", "mgba_libretro")
            .put("rom_uri", romUri.toString())
            .put(
                "intent",
                JSONObject()
                    .put("action", "android.intent.action.MAIN")
                    .put("package", "com.retroarch.aarch64")
                    .put("activity", "com.retroarch.browser.retroactivity.RetroActivityFuture")
                    .put(
                        "string_extras",
                        JSONObject()
                            .put("ROM", "{{rom.uri}}")
                            .put("LIBRETRO", "{{android.package_data}}/cores/{{core.library}}_android.so"),
                    )
                    .put("boolean_extras", JSONObject().put("QUITFOCUS", true))
                    .put("flags", JSONArray().put("grant-read-uri").put("new-task")),
            )

        IntentLauncher.launch(context, launch)
        val deadline = System.currentTimeMillis() + 10_000
        while (TestDocumentsProvider.openCount(context, relative) <= baselineOpenCount && System.currentTimeMillis() < deadline) {
            Thread.sleep(100)
        }
        assertTrue(
            "Pinned RetroArch did not open the granted SAF ROM",
            TestDocumentsProvider.openCount(context, relative) > baselineOpenCount,
        )
        Thread.sleep(12_000)
        val screenshot = requireNotNull(instrumentation.uiAutomation.takeScreenshot())
        val output = java.io.File(context.filesDir, "retroarch-real-launch.png")
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
        assertTrue("A system first-run surface covered the GBA frame", cornerBrightness < 300)
        assertTrue("RetroArch stayed on a black surface", nonBlack > 200)
        assertTrue("The GBA Hello frame was not visible", upperBright > 20)
        assertTrue("RetroArch did not render the GBA test frame", colors.size > 8)
    }
}
