package org.varkiv.agent

import org.json.JSONObject
import java.io.BufferedInputStream
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL

object HttpJson {
    private const val maxResponse = 8 * 1024 * 1024
    private val pathSegmentPattern = Regex("[A-Za-z0-9][A-Za-z0-9._~-]{0,127}")

    fun validateOrigin(value: String, allowHttp: Boolean): String {
        val uri = URI(value.trim())
        require((uri.scheme == "https" || uri.scheme == "http") && !uri.host.isNullOrBlank() && uri.userInfo == null && uri.query == null && uri.fragment == null && (uri.path.isNullOrEmpty() || uri.path == "/")) { "Invalid server origin" }
        require(uri.scheme != "http" || allowHttp) { "HTTP requires explicit LAN consent" }
        return value.trim().trimEnd('/')
    }

    fun pathSegment(value: String): String {
        require(pathSegmentPattern.matches(value)) { "Invalid protocol identifier" }
        return value
    }

    fun contentHash(value: String): String {
        val normalized = value.trim().lowercase()
        require(normalized.matches(Regex("[0-9a-f]{64}"))) { "Invalid protocol content hash" }
        return normalized
    }

    internal fun openConnection(
        target: String,
        token: String = "",
        method: String = "GET",
        accept: String = "application/json",
    ): HttpURLConnection {
        val connection = URL(target).openConnection() as HttpURLConnection
        connection.requestMethod = method
        connection.connectTimeout = 20_000
        connection.readTimeout = 120_000
        // A sync endpoint must never move a bearer token or save payload to a
        // redirect target chosen by a server, proxy, or captive portal.
        connection.instanceFollowRedirects = false
        connection.setRequestProperty("Accept", accept)
        if (token.isNotBlank()) connection.setRequestProperty("Authorization", "Bearer $token")
        return connection
    }

    fun request(method: String, target: String, token: String = "", body: JSONObject? = null, idempotency: String = ""): JSONObject {
        val connection = openConnection(target, token, method)
        if (idempotency.isNotBlank()) connection.setRequestProperty("Idempotency-Key", idempotency)
        if (body != null) {
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
            connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
        }
        return readJSONObject(connection)
    }

    internal fun readJSONObject(connection: HttpURLConnection): JSONObject {
        try {
            val status = connection.responseCode
            require(status in 200..299) {
                val stream = connection.errorStream ?: runCatching { connection.inputStream }.getOrNull()
                val message = if (stream == null) "" else readLimited(stream)
                "Server returned HTTP $status: ${sanitizeFailure(message)}"
            }
            val text = readLimited(connection.inputStream)
            return if (text.isBlank()) JSONObject() else JSONObject(text)
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(stream: java.io.InputStream): String {
        BufferedInputStream(stream).use { input ->
            val output = java.io.ByteArrayOutputStream()
            val buffer = ByteArray(8192)
            while (true) {
                if (Thread.currentThread().isInterrupted) throw java.io.InterruptedIOException("Request was cancelled")
                val count = input.read(buffer)
                if (count < 0) break
                require(output.size() + count <= maxResponse) { "Server response exceeded limit" }
                output.write(buffer, 0, count)
            }
            return output.toString(Charsets.UTF_8.name())
        }
    }

    private fun sanitizeFailure(value: String): String = try {
        val error = JSONObject(value).optJSONObject("error")
        error?.optString("code")?.take(80)?.ifBlank { null } ?: "request_failed"
    } catch (_: Exception) { "request_failed" }
}
