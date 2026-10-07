package io.openflux.desktop.model

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * How the platform reaches the core's link reading and making: the CLI's
 * `--parse-link` / `--make-link` on the desktop, package mobile's
 * ReadShareLink / MakeShareLink on Android. Both answer the core's
 * share.Result as JSON.
 */
interface CoreLinks {
    suspend fun read(link: String): String
    suspend fun make(configJson: String): String
}

/** [ShareLinkCodec] on the core: nothing about the link format lives in the app. */
class CoreShareLinkCodec(private val core: CoreLinks) : ShareLinkCodec {
    private val json = Json {
        ignoreUnknownKeys = true
        encodeDefaults = false
        explicitNulls = false
    }

    @Serializable
    private data class Result(
        val config: ShareConfig? = null,
        val context: String = "",
        val link: String = "",
        val error: String = "",
        val code: String = "",
        val param: String = "",
    )

    override suspend fun decode(link: String): ShareConfig =
        answer(core.read(link)).config ?: throw ShareLinkException("", detail = "ядро не вернуло настройки")

    override suspend fun encode(config: ShareConfig): String =
        answer(core.make(json.encodeToString(ShareConfig.serializer(), config))).link
            .ifEmpty { throw ShareLinkException("", detail = "ядро не вернуло ссылку") }

    private fun answer(text: String): Result {
        val result = runCatching { json.decodeFromString(Result.serializer(), text.trim()) }
            .getOrElse { throw ShareLinkException("", detail = "ядро не умеет работать со ссылками, обновите его") }
        if (result.error.isNotEmpty() || result.code.isNotEmpty()) throw ShareLinkException(result.code, result.param, result.error)
        return result
    }
}
