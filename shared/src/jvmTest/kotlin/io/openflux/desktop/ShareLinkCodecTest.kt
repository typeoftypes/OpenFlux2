package io.openflux.desktop

import io.openflux.desktop.model.CoreLinks
import io.openflux.desktop.model.CoreShareLinkCodec
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ProfileSource
import io.openflux.desktop.model.ShareConfig
import io.openflux.desktop.model.ShareLinkException
import io.openflux.desktop.model.ShareLinkMessages
import io.openflux.desktop.model.ShareTransport
import io.openflux.desktop.model.TransportType
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/** The app side of links: it hands everything to the core and words the core's reasons. */
class ShareLinkCodecTest {
    private class Core(private val answer: String) : CoreLinks {
        val asked = mutableListOf<String>()
        override suspend fun read(link: String): String { asked += link; return answer }
        override suspend fun make(configJson: String): String { asked += configJson; return answer }
    }

    private val secret = "0123456789abcdef".repeat(4)

    @Test
    fun takesTheConfigurationTheCoreRead() = runTest {
        val core = Core(
            """{"config":{"name":"Тест","negotiate":true,"secret":"$secret","context":"https://disk.yandex.ru/i/A",""" +
                """"transports":[{"type":"vyandex","url":"https://disk.yandex.ru/i/A","priority":100},""" +
                """{"type":"direct","priority":50,"dial":"203.0.113.10:8445"}]},"context":"https://disk.yandex.ru/i/A"}""",
        )
        val link = " openflux://v1/whatever\n"
        val config = CoreShareLinkCodec(core).decode(link)
        assertEquals(listOf(link), core.asked, "the link goes to the core untouched")
        assertEquals(ShareTransport("direct", priority = 50, dial = "203.0.113.10:8445"), config.transports[1])
        val profile = Profile.fromShare(config, "p", 0, ProfileSource.Link)
        assertEquals(TransportType.VYANDEX, profile.transport)
        assertEquals("https://disk.yandex.ru/i/A", profile.context)
        assertEquals(listOf(TransportType.DIRECT), profile.extras.map { it.type })
    }

    @Test
    fun handsTheConfigurationToTheCoreToMake() = runTest {
        val core = Core("""{"link":"openflux://v1/abc","context":"https://a"}""")
        val link = CoreShareLinkCodec(core).encode(ShareConfig(name = "Дом", secret = secret, transports = listOf(ShareTransport("yandex", url = "https://a"))))
        assertEquals("openflux://v1/abc", link)
        assertTrue("\"url\":\"https://a\"" in core.asked.single() && "\"name\":\"Дом\"" in core.asked.single())
    }

    @Test
    fun wordsTheCoresReason() = runTest {
        val e = assertFailsWith<ShareLinkException> {
            CoreShareLinkCodec(Core("""{"error":"share: unknown transport type \"pigeon\"","code":"unknown_transport","param":"pigeon"}""")).decode("x")
        }
        assertEquals("unknown_transport", e.code)
        assertEquals(ShareLinkMessages.text("unknown_transport", "pigeon"), e.message)
        assertTrue("pigeon" in e.message!!)
        // An old core has no --make-link: it answers with its banner.
        val old = assertFailsWith<ShareLinkException> { CoreShareLinkCodec(Core("written by p1neappleXpress\n")).encode(ShareConfig()) }
        assertTrue("обновите" in old.message!!)
    }

    /** A reason the core may give that the app has no words for would reach users as English. */
    @Test
    fun everyReasonHasItsOwnWords() {
        val fallback = ShareLinkMessages.text("", "", "")
        for (code in ShareLinkMessages.codes) assertNotEquals(fallback, ShareLinkMessages.text(code, "16", "detail"), code)
        assertEquals(ShareLinkMessages.codes.size, ShareLinkMessages.codes.toSet().size)
        assertTrue("MAX" in ShareLinkMessages.text("not_shareable", "oneme"))
    }
}
