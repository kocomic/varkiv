package org.varkiv.agent

import java.io.ByteArrayInputStream
import java.security.MessageDigest
import org.junit.Assert.assertEquals
import org.junit.Test

class DirectoryHashTest {
    @Test fun matchesTheServerCanonicalDirectoryHash() {
        val digest = MessageDigest.getInstance("SHA-256")
        var size = 0L
        size += updateCanonicalDirectoryHash(digest, "a.txt", ByteArrayInputStream("a".toByteArray()))
        size += updateCanonicalDirectoryHash(digest, "sub/b.bin", ByteArrayInputStream("bc".toByteArray()))
        assertEquals(3L, size)
        assertEquals("88d8addf66332b8f343177442cdb2bdaec20bcdbd6b3c62f676109adbd420151", digest.digest().joinToString("") { "%02x".format(it) })
    }
}
