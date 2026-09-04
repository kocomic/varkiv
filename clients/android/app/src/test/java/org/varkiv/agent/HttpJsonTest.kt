package org.varkiv.agent

import java.io.Closeable
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.SocketTimeoutException
import java.util.Collections
import kotlin.concurrent.thread
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class HttpJsonTest {
    @Test fun acceptsOnlyReviewedOrigins() {
        assertEquals("https://games.example.test", HttpJson.validateOrigin(" https://games.example.test/ ", false))
        assertEquals("http://192.0.2.20:8080", HttpJson.validateOrigin("http://192.0.2.20:8080", true))
    }

    @Test fun rejectsHttpWithoutConsentAndNonOrigins() {
        val invalid = listOf(
            "http://192.0.2.20:8080",
            "file:///private/library",
            "https://user:secret@games.example.test",
            "https://games.example.test/api",
            "https://games.example.test/?token=secret",
            "https://games.example.test/#fragment",
        )
        invalid.forEach { value ->
            assertThrows(value, IllegalArgumentException::class.java) { HttpJson.validateOrigin(value, false) }
        }
    }

    @Test fun acceptsOnlyOpaqueProtocolIdentifiersAsPathSegments() {
        assertEquals("0a1b2c3d-4e5f", HttpJson.pathSegment("0a1b2c3d-4e5f"))
        listOf("", "../session", "session/operation", "session?token=secret", "%2e%2e", " identifier").forEach { value ->
            assertThrows(value, IllegalArgumentException::class.java) { HttpJson.pathSegment(value) }
        }
        val digest = "A".repeat(64)
        assertEquals("a".repeat(64), HttpJson.contentHash(digest))
        listOf("", "a".repeat(63), "g".repeat(64), "a".repeat(65), "secret?hash=" + "a".repeat(64)).forEach { value ->
            assertThrows(value.take(40), IllegalArgumentException::class.java) { HttpJson.contentHash(value) }
        }
    }

    @Test fun authenticatedConnectionsNeverFollowRedirects() {
        lateinit var server: RawHttpServer
        server = RawHttpServer { path ->
            if (path == "/redirect") RawResponse(302, headers = mapOf("Location" to server.url("/sink")))
            else RawResponse(200, "{}".toByteArray())
        }
        try {
            val error = assertThrows(IllegalArgumentException::class.java) {
                val connection = HttpJson.openConnection(
                    server.url("/redirect"),
                    "fixture-token-must-not-move",
                )
                HttpJson.readJSONObject(connection)
            }
            server.awaitIdle()
            assertTrue(error.message.orEmpty().startsWith("Server returned HTTP 302"))
            assertEquals(listOf("/redirect"), server.paths.toList())
            assertEquals(1, server.authorizationHeaders.size)
            assertEquals("Bearer fixture-token-must-not-move", server.authorizationHeaders.single())
        } finally {
            server.close()
        }
    }

    @Test fun sharedJsonReaderRejectsOversizedUploadResponses() {
        val server = RawHttpServer {
            RawResponse(200, ByteArray(8 * 1024 * 1024 + 1) { 'x'.code.toByte() })
        }
        try {
            val error = assertThrows(IllegalArgumentException::class.java) {
                val connection = HttpJson.openConnection(server.url("/oversized"))
                HttpJson.readJSONObject(connection)
            }
            assertEquals("Server response exceeded limit", error.message)
            assertFalse(error.message.orEmpty().contains("fixture-token"))
        } finally {
            server.close()
        }
    }

    private data class RawResponse(
        val status: Int,
        val body: ByteArray = ByteArray(0),
        val headers: Map<String, String> = emptyMap(),
    )

    private class RawHttpServer(private val respond: (String) -> RawResponse) : Closeable {
        private val socket = ServerSocket().apply {
            reuseAddress = true
            bind(InetSocketAddress(InetAddress.getLoopbackAddress(), 0))
            soTimeout = 2_000
        }
        val paths: MutableList<String> = Collections.synchronizedList(mutableListOf())
        val authorizationHeaders: MutableList<String> = Collections.synchronizedList(mutableListOf())
        private val worker = thread(name = "http-json-fixture", isDaemon = true) {
            var handled = 0
            try {
                while (handled < 2) {
                    val client = try {
                        socket.accept()
                    } catch (_: SocketTimeoutException) {
                        break
                    }
                    client.use {
                        val reader = it.getInputStream().bufferedReader(Charsets.US_ASCII)
                        val request = reader.readLine()?.split(' ') ?: emptyList()
                        val path = request.getOrNull(1) ?: "/"
                        var authorization = ""
                        while (true) {
                            val line = reader.readLine() ?: break
                            if (line.isEmpty()) break
                            if (line.startsWith("Authorization:", ignoreCase = true)) authorization = line.substringAfter(':').trim()
                        }
                        paths += path
                        if (authorization.isNotEmpty()) authorizationHeaders += authorization
                        val response = respond(path)
                        val reason = if (response.status == 200) "OK" else "Found"
                        val head = buildString {
                            append("HTTP/1.1 ${response.status} $reason\r\n")
                            response.headers.forEach { (name, value) -> append("$name: $value\r\n") }
                            append("Content-Length: ${response.body.size}\r\n")
                            append("Connection: close\r\n\r\n")
                        }
                        try {
                            it.getOutputStream().use { output ->
                                output.write(head.toByteArray(Charsets.US_ASCII))
                                output.write(response.body)
                            }
                        } catch (_: Exception) {
                            // The oversized-response test intentionally closes
                            // the client once the configured limit is crossed.
                        }
                    }
                    handled++
                    socket.soTimeout = 300
                }
            } catch (_: Exception) {
                // close() interrupts accept after the assertion completes.
            }
        }

        fun url(path: String): String = "http://127.0.0.1:${socket.localPort}$path"

        fun awaitIdle() {
            worker.join(2_000)
        }

        override fun close() {
            socket.close()
            worker.join(2_000)
        }
    }
}
