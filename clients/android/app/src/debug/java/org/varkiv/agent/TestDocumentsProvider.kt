package org.varkiv.agent

import android.annotation.SuppressLint
import android.content.Context
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.CancellationSignal
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract
import android.provider.DocumentsProvider
import java.io.File

/**
 * A private, disposable SAF surface used only by instrumentation. Every byte
 * lives inside the debug app's files directory on the one-use AVD.
 */
class TestDocumentsProvider : DocumentsProvider() {
    override fun onCreate(): Boolean = requireNotNull(context).let {
        val root = storageRoot(it)
        check(root.isDirectory || root.mkdirs()) { "Unable to initialize test SAF root" }
        true
    }

    override fun queryRoots(projection: Array<out String>?): Cursor {
        val columns = projection ?: ROOT_COLUMNS
        return MatrixCursor(columns).apply {
            newRow().apply {
                add(DocumentsContract.Root.COLUMN_ROOT_ID, ROOT_ID)
                add(DocumentsContract.Root.COLUMN_DOCUMENT_ID, ROOT_ID)
                add(DocumentsContract.Root.COLUMN_TITLE, "Varkiv AVD fixture")
                add(DocumentsContract.Root.COLUMN_FLAGS, DocumentsContract.Root.FLAG_SUPPORTS_CREATE or DocumentsContract.Root.FLAG_LOCAL_ONLY)
                add(DocumentsContract.Root.COLUMN_MIME_TYPES, "*/*")
                add(DocumentsContract.Root.COLUMN_AVAILABLE_BYTES, 16L * 1024 * 1024)
            }
        }
    }

    override fun queryDocument(documentId: String, projection: Array<out String>?): Cursor {
        val columns = projection ?: DOCUMENT_COLUMNS
        val file = fileFor(documentId)
        require(file.exists()) { "Unknown test document" }
        return MatrixCursor(columns).apply { addDocument(newRow(), file, documentId) }
    }

    override fun queryChildDocuments(parentDocumentId: String, projection: Array<out String>?, sortOrder: String?): Cursor {
        val columns = projection ?: DOCUMENT_COLUMNS
        val parent = fileFor(parentDocumentId)
        require(parent.isDirectory) { "Test document is not a directory" }
        return MatrixCursor(columns).apply {
            parent.listFiles().orEmpty().sortedBy { it.name }.forEach { child ->
                addDocument(newRow(), child, documentId(child))
            }
        }
    }

    @SuppressLint("ApplySharedPref")
    override fun openDocument(documentId: String, mode: String, signal: CancellationSignal?): ParcelFileDescriptor {
        val file = fileFor(documentId)
        require(file.isFile) { "Test document is not a file" }
        val testContext = requireNotNull(context)
        // Instrumentation reads this immediately after the cross-component open.
        check(testContext.getSharedPreferences(OPEN_PREFS, Context.MODE_PRIVATE).edit()
            .putInt(documentId, openCountByDocumentId(testContext, documentId) + 1)
            .commit()) { "Unable to record test document open" }
        return ParcelFileDescriptor.open(file, ParcelFileDescriptor.parseMode(mode))
    }

    override fun createDocument(parentDocumentId: String, mimeType: String, displayName: String): String {
        requireSafeName(displayName)
        val parent = fileFor(parentDocumentId)
        require(parent.isDirectory) { "Test parent is not a directory" }
        val target = File(parent, displayName)
        require(!target.exists()) { "Test document already exists" }
        val created = if (mimeType == DocumentsContract.Document.MIME_TYPE_DIR) target.mkdir() else target.createNewFile()
        check(created) { "Unable to create test document" }
        return documentId(target)
    }

    override fun renameDocument(documentId: String, displayName: String): String {
        requireSafeName(displayName)
        val source = fileFor(documentId)
        require(documentId != ROOT_ID) { "Cannot rename test root" }
        val target = File(requireNotNull(source.parentFile), displayName)
        require(!target.exists()) { "Test rename would overwrite a document" }
        check(source.renameTo(target)) { "Unable to rename test document" }
        return documentId(target)
    }

    override fun deleteDocument(documentId: String) {
        val target = fileFor(documentId)
        require(target != storageRoot(requireNotNull(context))) { "Cannot delete test root" }
        check(if (target.isDirectory) target.deleteRecursively() else target.delete()) { "Unable to delete test document" }
    }

    override fun isChildDocument(parentDocumentId: String, documentId: String): Boolean {
        val parent = fileFor(parentDocumentId).canonicalFile
        val child = fileFor(documentId).canonicalFile
        return child.path.startsWith(parent.path + File.separator)
    }

    private fun addDocument(row: MatrixCursor.RowBuilder, file: File, id: String) {
        val directory = file.isDirectory
        val flags = if (directory) {
            DocumentsContract.Document.FLAG_DIR_SUPPORTS_CREATE or DocumentsContract.Document.FLAG_SUPPORTS_DELETE or DocumentsContract.Document.FLAG_SUPPORTS_RENAME
        } else {
            DocumentsContract.Document.FLAG_SUPPORTS_WRITE or DocumentsContract.Document.FLAG_SUPPORTS_DELETE or DocumentsContract.Document.FLAG_SUPPORTS_RENAME
        }
        row.add(DocumentsContract.Document.COLUMN_DOCUMENT_ID, id)
        row.add(DocumentsContract.Document.COLUMN_DISPLAY_NAME, if (id == ROOT_ID) "root" else file.name)
        row.add(DocumentsContract.Document.COLUMN_MIME_TYPE, if (directory) DocumentsContract.Document.MIME_TYPE_DIR else "application/octet-stream")
        row.add(DocumentsContract.Document.COLUMN_FLAGS, flags)
        row.add(DocumentsContract.Document.COLUMN_SIZE, if (directory) 0L else file.length())
        row.add(DocumentsContract.Document.COLUMN_LAST_MODIFIED, file.lastModified())
    }

    private fun fileFor(documentId: String): File {
        require(documentId == ROOT_ID || documentId.startsWith("$ROOT_ID/")) { "Unsafe test document ID" }
        val root = storageRoot(requireNotNull(context)).canonicalFile
        val relative = documentId.removePrefix(ROOT_ID).trimStart('/')
        val file = if (relative.isEmpty()) root else File(root, relative).canonicalFile
        require(file == root || file.path.startsWith(root.path + File.separator)) { "Test document escaped its root" }
        return file
    }

    private fun documentId(file: File): String {
        val root = storageRoot(requireNotNull(context)).canonicalFile
        val target = file.canonicalFile
        require(target == root || target.path.startsWith(root.path + File.separator))
        return if (target == root) ROOT_ID else "$ROOT_ID/${target.relativeTo(root).invariantSeparatorsPath}"
    }

    private fun requireSafeName(value: String) {
        require(value.isNotBlank() && value != "." && value != ".." && '/' !in value && '\\' !in value && '\u0000' !in value)
    }

    companion object {
        const val AUTHORITY = "org.varkiv.agent.test.documents"
        private const val ROOT_ID = "root"
        private const val OPEN_PREFS = "varkiv-e2e-document-opens"
        private val ROOT_COLUMNS = arrayOf(
            DocumentsContract.Root.COLUMN_ROOT_ID,
            DocumentsContract.Root.COLUMN_DOCUMENT_ID,
            DocumentsContract.Root.COLUMN_TITLE,
            DocumentsContract.Root.COLUMN_FLAGS,
            DocumentsContract.Root.COLUMN_MIME_TYPES,
            DocumentsContract.Root.COLUMN_AVAILABLE_BYTES,
        )
        private val DOCUMENT_COLUMNS = arrayOf(
            DocumentsContract.Document.COLUMN_DOCUMENT_ID,
            DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE,
            DocumentsContract.Document.COLUMN_FLAGS,
            DocumentsContract.Document.COLUMN_SIZE,
            DocumentsContract.Document.COLUMN_LAST_MODIFIED,
        )

        fun treeUri(relative: String = ""): Uri {
            val id = documentIdForRelative(relative)
            return DocumentsContract.buildTreeDocumentUri(AUTHORITY, id)
        }

        fun documentUri(relative: String): Uri {
            val id = documentIdForRelative(relative)
            require(id != ROOT_ID) { "A test fixture document path is required" }
            return DocumentsContract.buildDocumentUri(AUTHORITY, id)
        }

        fun reset(context: Context, files: Map<String, ByteArray>) {
            clear(context)
            val root = storageRoot(context)
            check(root.isDirectory || root.mkdirs()) { "Unable to create test SAF root" }
            files.toSortedMap().forEach { (relative, bytes) ->
                val target = fixtureFile(context, relative)
                val parent = requireNotNull(target.parentFile)
                check(parent.isDirectory || parent.mkdirs()) { "Unable to create test SAF directory" }
                target.writeBytes(bytes)
            }
        }

        fun write(context: Context, relative: String, bytes: ByteArray) {
            val target = fixtureFile(context, relative)
            require(target.isFile) { "Test SAF file is unavailable" }
            target.writeBytes(bytes)
        }

        fun read(context: Context, relative: String): ByteArray = fixtureFile(context, relative).readBytes()

        fun openCount(context: Context, relative: String): Int = openCountByDocumentId(context, documentIdForRelative(relative))

        fun names(context: Context, relative: String = ""): List<String> = fixtureFile(context, relative, allowRoot = true)
            .listFiles().orEmpty().map { it.name }.sorted()

        @SuppressLint("ApplySharedPref")
        fun clear(context: Context) {
            val root = storageRoot(context)
            if (root.exists()) check(root.deleteRecursively()) { "Unable to clear test SAF root" }
            // A reset must be fully visible before the next instrumentation step.
            check(context.getSharedPreferences(OPEN_PREFS, Context.MODE_PRIVATE).edit().clear().commit()) {
                "Unable to reset test document observations"
            }
        }

        private fun openCountByDocumentId(context: Context, documentId: String): Int =
            context.getSharedPreferences(OPEN_PREFS, Context.MODE_PRIVATE).getInt(documentId, 0)

        private fun documentIdForRelative(relative: String): String {
            val parts = safeRelativeParts(relative)
            return if (parts.isEmpty()) ROOT_ID else "$ROOT_ID/${parts.joinToString("/")}"
        }

        private fun fixtureFile(context: Context, relative: String, allowRoot: Boolean = false): File {
            val parts = safeRelativeParts(relative)
            require(allowRoot || parts.isNotEmpty()) { "A test fixture file path is required" }
            val root = storageRoot(context).canonicalFile
            val target = parts.fold(root) { current, part -> File(current, part) }.canonicalFile
            require(target == root || target.path.startsWith(root.path + File.separator)) { "Test fixture escaped its root" }
            return target
        }

        private fun safeRelativeParts(relative: String): List<String> {
            require(relative == relative.trim() && !relative.startsWith('/') && !relative.endsWith('/') && '\\' !in relative) {
                "Unsafe test fixture path"
            }
            if (relative.isEmpty()) return emptyList()
            return relative.split('/').also { parts ->
                require(parts.all { it.isNotBlank() && it != "." && it != ".." && '\u0000' !in it }) { "Unsafe test fixture path" }
            }
        }

        private fun storageRoot(context: Context): File = File(context.filesDir, "varkiv-e2e-documents")
    }
}
