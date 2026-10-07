package io.openflux.desktop

import io.openflux.desktop.model.ShareConfig
import io.openflux.desktop.model.ShareLinkCodec
import kotlinx.serialization.json.Json

/**
 * A stand-in for the core in tests that only need a link to carry a
 * configuration from one place to another: not the real format (the core
 * owns that; CoreLinksContractTest checks the real thing).
 */
class FakeShareLinkCodec : ShareLinkCodec {
    private val json = Json { ignoreUnknownKeys = true }

    override suspend fun encode(config: ShareConfig): String =
        ShareLinkCodec.PREFIX + "test:" + json.encodeToString(ShareConfig.serializer(), config)

    override suspend fun decode(link: String): ShareConfig =
        json.decodeFromString(ShareConfig.serializer(), link.removePrefix(ShareLinkCodec.PREFIX + "test:"))
}
