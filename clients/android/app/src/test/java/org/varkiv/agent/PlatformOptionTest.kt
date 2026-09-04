package org.varkiv.agent

import org.junit.Assert.assertEquals
import org.junit.Test

class PlatformOptionTest {
    @Test
    fun platformLabelsUseReadableNamesWithoutExposingInternalIds() {
        val option = PlatformOption("gba", "Nintendo Game Boy Advance", "Game Boy Advance")
        assertEquals("Game Boy Advance", option.label("zh"))
        assertEquals("Game Boy Advance", option.label("zh-TW"))
        assertEquals("Nintendo Game Boy Advance", option.label("en"))
        assertEquals("Nintendo Game Boy Advance", option.label("ja"))
    }

    @Test
    fun nonChineseLocalesDoNotFallBackToTheInternalIdentity() {
        assertEquals("Open Handheld", PlatformOption("custom-handheld", "Open Handheld").label("en"))
    }
}
