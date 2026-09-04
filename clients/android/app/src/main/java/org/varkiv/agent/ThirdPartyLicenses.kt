package org.varkiv.agent

object ThirdPartyLicenses {
    const val NOTICES_ASSET = "THIRD_PARTY_NOTICES.md"
    const val APACHE_ASSET = "Apache-2.0.txt"

    fun compose(notices: String, apacheLicense: String, apacheHeading: String): String = buildString {
        append(notices.trim())
        append("\n\n")
        append(apacheHeading.trim())
        append("\n")
        append("=".repeat(apacheHeading.trim().length.coerceAtLeast(3)))
        append("\n\n")
        append(apacheLicense.trim())
        append('\n')
    }
}
