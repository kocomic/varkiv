package org.varkiv.agent

import android.content.ContentResolver
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract
import java.io.InputStream
import java.security.MessageDigest

data class SafDocument(val uri: Uri, val id: String, val name: String, val mime: String, val size: Long, val modified: Long) {
    val isDirectory: Boolean get() = mime == DocumentsContract.Document.MIME_TYPE_DIR
}

class SafTree(private val resolver: ContentResolver, val treeUri: Uri) {
    val root: SafDocument by lazy {
        val id = DocumentsContract.getTreeDocumentId(treeUri)
        stat(DocumentsContract.buildDocumentUriUsingTree(treeUri, id)) ?: error("Authorized tree is unavailable")
    }

    fun stat(uri: Uri): SafDocument? {
        val projection = arrayOf(DocumentsContract.Document.COLUMN_DOCUMENT_ID, DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE, DocumentsContract.Document.COLUMN_SIZE, DocumentsContract.Document.COLUMN_LAST_MODIFIED)
        resolver.query(uri, projection, null, null, null)?.use { cursor ->
            if (!cursor.moveToFirst()) return null
            return SafDocument(uri, cursor.getString(0), cursor.getString(1) ?: "", cursor.getString(2) ?: "application/octet-stream",
                if (cursor.isNull(3)) 0 else cursor.getLong(3), if (cursor.isNull(4)) 0 else cursor.getLong(4))
        }
        return null
    }

    fun children(parent: SafDocument): List<SafDocument> {
        require(parent.isDirectory)
        val uri = DocumentsContract.buildChildDocumentsUriUsingTree(treeUri, parent.id)
        val projection = arrayOf(DocumentsContract.Document.COLUMN_DOCUMENT_ID, DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE, DocumentsContract.Document.COLUMN_SIZE, DocumentsContract.Document.COLUMN_LAST_MODIFIED)
        val result = mutableListOf<SafDocument>()
        resolver.query(uri, projection, null, null, null)?.use { cursor ->
            while (cursor.moveToNext()) {
                val id = cursor.getString(0)
                result += SafDocument(DocumentsContract.buildDocumentUriUsingTree(treeUri, id), id, cursor.getString(1) ?: "",
                    cursor.getString(2) ?: "application/octet-stream", if (cursor.isNull(3)) 0 else cursor.getLong(3), if (cursor.isNull(4)) 0 else cursor.getLong(4))
            }
        }
        return result
    }

    fun find(relative: String): SafDocument? {
        val parts = safeParts(relative)
        var current = root
        for (part in parts) {
            if (!current.isDirectory) return null
            current = children(current).firstOrNull { it.name == part } ?: return null
        }
        return current
    }

    fun parentAndName(relative: String): Pair<SafDocument, String> {
        val parts = safeParts(relative)
        require(parts.isNotEmpty()) { "A binding cannot target the entire authorized tree" }
        var parent = root
        for (part in parts.dropLast(1)) {
            parent = children(parent).firstOrNull { it.name == part && it.isDirectory }
                ?: create(parent, DocumentsContract.Document.MIME_TYPE_DIR, part)
        }
        return parent to parts.last()
    }

    fun create(parent: SafDocument, mime: String, name: String): SafDocument {
        require(safeName(name))
        require(children(parent).none { it.name == name }) { "Refusing to overwrite an existing document" }
        val uri = DocumentsContract.createDocument(resolver, parent.uri, mime, name) ?: error("Document provider refused create")
        return stat(uri) ?: error("Created document is unavailable")
    }

    fun rename(document: SafDocument, name: String): SafDocument {
        require(safeName(name))
        val uri = DocumentsContract.renameDocument(resolver, document.uri, name) ?: error("Document provider refused rename")
        return stat(uri) ?: error("Renamed document is unavailable")
    }

    fun deleteOwned(document: SafDocument, expectedPrefix: String) {
        require(document.name.startsWith(expectedPrefix)) { "Refusing to remove a document not proven to be app-owned temporary output" }
        DocumentsContract.deleteDocument(resolver, document.uri)
    }

    fun open(document: SafDocument): InputStream = resolver.openInputStream(document.uri) ?: error("Document is unavailable")

    // Returns null when a document provider cannot offer seekable local-style
    // access. Callers then perform a full SHA-256 instead of trusting metadata.
    fun quickFileSignal(document: SafDocument, chunkSize: Int = 512): String? {
        if (document.isDirectory || document.size <= 0) return null
        return try {
            val descriptor = resolver.openFileDescriptor(document.uri, "r") ?: return null
            ParcelFileDescriptor.AutoCloseInputStream(descriptor).use { input ->
                val digest = MessageDigest.getInstance("SHA-256")
                digest.update("${document.size}\u0000${document.modified}\u0000".toByteArray())
                val offsets = linkedSetOf(0L)
                if (document.size > chunkSize) {
                    offsets += maxOf(0, document.size / 2 - chunkSize / 2)
                    offsets += maxOf(0, document.size - chunkSize)
                }
                val buffer = ByteArray(chunkSize)
                offsets.forEach { offset ->
                    input.channel.position(offset)
                    val count = input.read(buffer)
                    digest.update("$offset\u0000".toByteArray())
                    if (count > 0) digest.update(buffer, 0, count)
                    digest.update(0.toByte())
                }
                digest.digest().joinToString("") { "%02x".format(it) }
            }
        } catch (_: Exception) { null }
    }
    fun write(document: SafDocument, input: InputStream) {
        resolver.openOutputStream(document.uri, "wt")?.use { output ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                if (Thread.currentThread().isInterrupted) throw java.io.InterruptedIOException("Write was cancelled")
                val count = input.read(buffer)
                if (count < 0) break
                output.write(buffer, 0, count)
            }
        } ?: error("Document is not writable")
    }

    fun walkFiles(start: SafDocument = root, maxFiles: Int = 10_000): List<Pair<String, SafDocument>> {
        val output = mutableListOf<Pair<String, SafDocument>>()
        fun visit(document: SafDocument, relative: String) {
            require(output.size <= maxFiles) { "Authorized tree exceeds the file-count limit" }
            if (!document.isDirectory) { output += relative to document; return }
            children(document).sortedBy { it.name }.forEach { child -> visit(child, if (relative.isEmpty()) child.name else "$relative/${child.name}") }
        }
        visit(start, "")
        require(output.size <= maxFiles) { "Authorized tree exceeds the file-count limit" }
        return output
    }

    private fun safeParts(value: String): List<String> {
        val normalized = value.trim().replace('\\', '/').trim('/')
        if (normalized.isEmpty()) return emptyList()
        val parts = normalized.split('/')
        require(parts.all(::safeName)) { "Unsafe or unresolved SAF-relative path" }
        return parts
    }

    private fun safeName(value: String): Boolean = value.isNotBlank() && value != "." && value != ".." && !value.contains('/') && !value.contains('\\') && !value.contains('\u0000')
}

fun hashStream(input: InputStream): Pair<String, Long> {
    val digest = MessageDigest.getInstance("SHA-256")
    var size = 0L
    val buffer = ByteArray(64 * 1024)
    input.use {
        while (true) {
            if (Thread.currentThread().isInterrupted) throw java.io.InterruptedIOException("Hash was cancelled")
            val count = it.read(buffer)
            if (count < 0) break
            digest.update(buffer, 0, count); size += count
        }
    }
    return digest.digest().joinToString("") { "%02x".format(it) } to size
}

fun hashDirectory(tree: SafTree, directory: SafDocument): Pair<String, Long> {
    require(directory.isDirectory)
    val digest = MessageDigest.getInstance("SHA-256")
    var size = 0L
    tree.walkFiles(directory).sortedBy { it.first }.forEach { (relative, document) ->
        tree.open(document).use { input -> size += updateCanonicalDirectoryHash(digest, relative, input) }
    }
    return digest.digest().joinToString("") { "%02x".format(it) } to size
}

fun quickDirectorySignal(tree: SafTree, directory: SafDocument): Pair<String, Long>? {
    require(directory.isDirectory)
    val digest = MessageDigest.getInstance("SHA-256")
    var size = 0L
    tree.walkFiles(directory).sortedBy { it.first }.forEach { (relative, document) ->
        val signal = tree.quickFileSignal(document) ?: return null
        size += document.size
        digest.update(relative.replace('\\', '/').toByteArray(Charsets.UTF_8))
        digest.update(0.toByte())
        digest.update(signal.toByteArray(Charsets.UTF_8))
        digest.update(0.toByte())
    }
    return digest.digest().joinToString("") { "%02x".format(it) } to size
}

internal fun updateCanonicalDirectoryHash(digest: MessageDigest, relative: String, input: InputStream): Long {
    digest.update(relative.replace('\\', '/').toByteArray(Charsets.UTF_8))
    digest.update(0.toByte())
    var size = 0L
    val buffer = ByteArray(64 * 1024)
    while (true) {
        if (Thread.currentThread().isInterrupted) throw java.io.InterruptedIOException("Hash was cancelled")
        val count = input.read(buffer)
        if (count < 0) break
        digest.update(buffer, 0, count)
        size += count
    }
    return size
}
