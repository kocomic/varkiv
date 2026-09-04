package org.varkiv.agent

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ROMHashRecordTest {
    private val checksum = "a".repeat(64)
    private val signal = "b".repeat(64)

    @Test fun reusesOnlyExactRecentContentSignal() {
        val record = ROMHashRecord(1024, 123456, checksum, "file", signal, 1_000)
        assertTrue(record.matches("file", 1024, 123456, signal, 1_100))
        assertFalse(record.matches("directory", 1024, 123456, signal, 1_100))
        assertFalse(record.matches("file", 1025, 123456, signal, 1_100))
        assertFalse(record.matches("file", 1024, 123457, signal, 1_100))
        assertFalse(record.matches("file", 1024, 123456, "c".repeat(64), 1_100))
    }

    @Test fun refusesExpiredFutureAndMalformedRecords() {
        assertFalse(ROMHashRecord(1, 2, checksum, "file", signal, 0).matches("file", 1, 2, signal, 1_000))
        assertFalse(ROMHashRecord(1, 2, checksum, "file", signal, 2_000).matches("file", 1, 2, signal, 1_000))
        assertFalse(ROMHashRecord(1, 2, checksum, "file", signal, 1).matches("file", 1, 2, signal, 90_000))
        assertFalse(ROMHashRecord(1, 2, "not-a-digest", "file", signal, 1).matches("file", 1, 2, signal, 2))
    }
}
