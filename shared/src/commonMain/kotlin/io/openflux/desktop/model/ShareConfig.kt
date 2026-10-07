package io.openflux.desktop.model

import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.Serializable

/**
 * The JSON inside an `openflux://v1/` link, field for field as the core's
 * share.Config writes it. Only the shape: the core reads, checks and makes
 * links ([ShareLinkCodec]).
 */
@OptIn(ExperimentalSerializationApi::class)
@Serializable
data class ShareConfig(
    val name: String = "",
    val negotiate: Boolean = false,
    val codec: String = "",
    val secret: String = "",
    val context: String = "",
    /** "" is the classic tunnel; "stream" is the mode without a server (a PHP node on a web host). */
    val mode: String = "",
    @EncodeDefault val transports: List<ShareTransport> = emptyList(),
) {
    val isStream: Boolean get() = mode == MODE_STREAM

    companion object {
        const val MODE_STREAM = "stream"
    }
}

@Serializable
data class ShareTransport(
    val type: String,
    val name: String = "",
    val url: String = "",
    val priority: Int = 0,
    val dial: String = "",
)

/**
 * Reads and makes `openflux://v1/` links through the core, the one reading
 * and making every client uses, so a link means the same on every device.
 * A link the core refuses is a [ShareLinkException].
 */
interface ShareLinkCodec {
    suspend fun encode(config: ShareConfig): String
    suspend fun decode(link: String): ShareConfig

    companion object {
        const val PREFIX = "openflux://v1/"
    }
}

/**
 * A link the core would not read or make: [code] is its reason (the core's
 * share.Code*), [param] the value it is about, [detail] the core's own
 * English text; the message is the app's words for it.
 */
class ShareLinkException(val code: String, val param: String = "", val detail: String = "") :
    IllegalArgumentException(ShareLinkMessages.text(code, param, detail))
