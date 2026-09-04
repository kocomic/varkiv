package org.varkiv.agent

import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Test

class SharedSaveStreamTest {
    @Test
    fun identicalSharedTargetAndSnapshotAreAccepted() {
        requireCompatibleSharedSaveSet(
            "content://fixture/tree", "PCSX2/memcards", "fixture-hash",
            "content://fixture/tree", "PCSX2/memcards", "fixture-hash",
        )
    }

    @Test
    fun differentSharedTargetIsRejectedWithoutLeakingUrisOrPaths() {
        val error = assertThrows(IllegalArgumentException::class.java) {
            requireCompatibleSharedSaveSet(
                "content://private/tree-a", "private/slot-a", "fixture-hash",
                "content://private/tree-b", "private/slot-b", "fixture-hash",
            )
        }
        assertFalse(error.message.orEmpty().contains("content://"))
        assertFalse(error.message.orEmpty().contains("slot-a"))
        assertFalse(error.message.orEmpty().contains("slot-b"))
    }

    @Test
    fun differentSharedSnapshotIsRejected() {
        assertThrows(IllegalArgumentException::class.java) {
            requireCompatibleSharedSaveSet(
                "content://fixture/tree", "Flycast/data", "snapshot-a",
                "content://fixture/tree", "Flycast/data", "snapshot-b",
            )
        }
    }
}
