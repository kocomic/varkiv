package org.varkiv.agent

import java.io.ByteArrayInputStream
import org.junit.Assert.assertEquals
import org.junit.Test

class HashStreamTest {
    @Test fun hashesTheExactByteStreamAndCountsBytes() {
        val content = "android-saf-test".toByteArray()
        val (checksum, size) = hashStream(ByteArrayInputStream(content))
        assertEquals(content.size.toLong(), size)
        assertEquals("c55676fb2850e8e5a46ca7d34c8a44291bdcc157352cb72d5d12a8c312ffd8b0", checksum)
    }

    @Test fun emptyStreamHasTheStandardSha256() {
        val (checksum, size) = hashStream(ByteArrayInputStream(byteArrayOf()))
        assertEquals(0, size)
        assertEquals("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", checksum)
    }
}
