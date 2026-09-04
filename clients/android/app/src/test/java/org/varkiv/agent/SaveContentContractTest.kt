package org.varkiv.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Test

class SaveContentContractTest {
    @Test fun singleFileLogicalRoleMatchesTheDesktopAgentWithoutDisclosingTheRomStem() {
        assertEquals(true, isSingleFileSaveBinding("file", "driver-defined"))
        assertEquals(true, isSingleFileSaveBinding("", "single-file"))
        assertEquals(false, isSingleFileSaveBinding("directory", "single-file"))
        val first = stableSingleFileLogicalPath("renamed-only-on-android.SRM")
        val second = stableSingleFileLogicalPath("different-name-on-windows.srm")
        assertEquals("primary.srm", first)
        assertEquals(first, second)
        assertFalse(first.contains("renamed"))
        assertEquals("primary", stableSingleFileLogicalPath("private-save-name"))
        assertEquals("primary", stableSingleFileLogicalPath("private-save-name.bad?"))
        assertEquals("primary", stableSingleFileLogicalPath("private-save-name.状态"))
    }

    @Test fun contentHashMatchesTheDesktopAgentFixedVector() {
        val checksum = "1a20d1ac4c9f15e47bef3c205377617bf199e1961f6e5928e928345e2bb4b625"
        val hash = saveSetContentHash(listOf(SaveContentIdentity("primary.srm", checksum, 20)))
        assertEquals("43ab9a1bc5bcc39d766bee0acbf59e69cc11f73d092898f9cea42bf71c2ceb59", hash)
    }

    @Test fun portableLogicalPathsMatchTheHostIndependentServerContract() {
        mapOf(
            "primary.srm" to "primary.srm",
            "cards/Mcd001.ps2" to "cards/Mcd001.ps2",
            "セーブ/slot 1.dat" to "セーブ/slot 1.dat",
            "emoji/🎮.sav" to "emoji/🎮.sav",
        ).forEach { (input, expected) -> assertEquals(expected, portableSaveLogicalPath(input)) }
        listOf(
            "", "/absolute", "C:\\private\\save.srm", "C:/private/save.srm", "../escape", "a/../escape",
            "double//separator", "trailing/", " leading", "trailing ", "slot.", "CON", "nul.dat", "COM1.bin",
            "bad?.sav", "bad\u0000name", "line\nbreak.sav",
            "bad\uD800name",
        ).forEach { input ->
            assertThrows(input.take(40), IllegalArgumentException::class.java) { portableSaveLogicalPath(input) }
        }
    }

    @Test fun aggregateDownloadBudgetMatchesTheDesktopAgentContract() {
        assertEquals(3L, remainingMobileSaveDownloadBudget(MOBILE_MAX_SAVE_BYTES - 3, 3, MOBILE_MAX_SAVE_FILES))
        listOf(
            Triple(MOBILE_MAX_SAVE_BYTES - 3, 4L, 1),
            Triple(MOBILE_MAX_SAVE_BYTES + 1, -1L, 1),
            Triple(-1L, -1L, 1),
            Triple(0L, -1L, MOBILE_MAX_SAVE_FILES + 1),
        ).forEach { (total, declared, files) ->
            assertThrows(IllegalArgumentException::class.java) { remainingMobileSaveDownloadBudget(total, declared, files) }
        }
    }
}
