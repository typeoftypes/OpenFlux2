package io.openflux.desktop

import io.openflux.desktop.model.AppSettings
import io.openflux.desktop.model.ConnectionMode
import io.openflux.desktop.model.CoreConfig
import io.openflux.desktop.model.CorePaths
import io.openflux.desktop.model.PhpHosts
import io.openflux.desktop.model.PhpMessages
import io.openflux.desktop.model.PhpNodeRef
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ProfileSource
import io.openflux.desktop.model.ShareConfig
import io.openflux.desktop.model.ShareLinkMessages
import io.openflux.desktop.model.ShareTransport
import io.openflux.desktop.model.TransportType
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** The mode without a server: the profile, its link, the core's command line, and the words. */
class StreamProfileTest {
    private val room = "https://interview.cups.online/live-coding/?room=0a1b2c3d-1111-2222-3333-444455556666"
    private val doc = "https://cloud.mail.ru/public/Vuri/d5nuZ5aQp"
    private val paths = CorePaths(null, "C:/rt/p.conf", "C:/cfg/cookies/p.json", "C:/rt/ipc.sock")

    private fun cups() = Profile(
        id = "s", name = "Free", transport = TransportType.CUPSONLINE, value = room, stream = true,
        phpNode = PhpNodeRef("https://mysite.42web.io", "tok"),
    )

    @Test
    fun aStreamProfileNeedsACarrierThePhpExitHas() {
        assertTrue(cups().problems().isEmpty())
        assertTrue(cups().copy(transport = TransportType.MAILRU, value = doc).problems().isEmpty())
        assertTrue(cups().copy(value = "https://example.com").problems().single().contains("cups.online"))
        assertTrue(cups().copy(transport = TransportType.MAILRU, value = room).problems().single().contains("Mail.ru"))
        assertTrue(cups().copy(transport = TransportType.VYANDEX, value = "https://disk.yandex.ru/i/x").problems().single().contains("cups.online или Mail.ru"))
        assertTrue(cups().copy(secret = "f".repeat(32)).problems().single().contains("нет ключа"))
        assertTrue(cups().copy(session = true).problems().single().contains("нет ключа"))
        assertEquals("Без сервера · Cups", cups().summary)
    }

    @Test
    fun theLinkCarriesTheModeButNeverTheNodesToken() {
        val share = cups().toShare().getOrThrow()
        assertEquals("stream", share.mode)
        assertEquals(listOf(ShareTransport(type = "cupsonline", url = room)), share.transports)
        assertEquals("", share.secret)
        assertFalse(Json.encodeToString(ShareConfig.serializer(), share).contains("tok"), "the node's token must stay out of a link")

        val back = Profile.fromShare(share, "n", 5, ProfileSource.Qr)
        assertTrue(back.stream)
        assertEquals(TransportType.CUPSONLINE, back.transport)
        assertEquals(room, back.value)
        assertNull(back.phpNode, "a device that scanned the link only connects: it has no control of the node")
        assertEquals(cups().copy(id = "n", source = ProfileSource.Qr, createdAt = 5, phpNode = null), back)

        assertTrue(cups().copy(value = "nope").toShare().isFailure, "a broken profile makes no link")
    }

    @Test
    fun aClassicLinkStillMakesAClassicProfile() {
        val cfg = ShareConfig(name = "Old", transports = listOf(ShareTransport("mailru", url = doc)))
        val p = Profile.fromShare(cfg, "x", 1, ProfileSource.Link)
        assertFalse(p.stream)
        assertEquals(TransportType.MAILRU, p.transport)
        assertEquals("", cfg.mode)
    }

    @Test
    fun aStreamLinkMustHaveExactlyOneTransport() {
        val two = ShareConfig(mode = "stream", transports = listOf(ShareTransport("mailru", url = doc), ShareTransport("cupsonline", url = room)))
        assertFailsWith<IllegalArgumentException> { Profile.fromShare(two, "x", 1, ProfileSource.Link) }
    }

    @Test
    fun coreArgumentsForTheStreamMode() {
        val proxy = CoreConfig.build(cups(), AppSettings(socksPort = 1090), paths)
        assertEquals(
            listOf(
                "--role=client", "--mode=stream", "--inbound=socks5", "--socks5=127.0.0.1:1090", "--http-proxy=127.0.0.1:1091",
                "--transport=cupsonline", "--url=$room", "--ipc-socket=C:/rt/ipc.sock",
            ),
            proxy.arguments,
        )
        assertNull(proxy.conf, "there is no .conf in this mode")
        assertEquals("127.0.0.1:1090", proxy.socksAddress)
        assertEquals("127.0.0.1:1091", proxy.httpProxyAddress)
        assertFalse(proxy.usesIpc)

        val tun = CoreConfig.build(cups(), AppSettings(fullTunnel = true, debugLevel = 2), paths)
        assertEquals(
            listOf("--role=client", "--mode=stream", "--inbound=tun", "--transport=cupsonline", "--url=$room", "--debug=2"),
            tun.arguments,
        )
        assertNull(tun.socksAddress)

        val mailru = CoreConfig.build(cups().copy(transport = TransportType.MAILRU, value = doc), AppSettings(), paths)
        assertTrue("--transport=mailru" in mailru.arguments && "--url=$doc" in mailru.arguments)
        for (forbidden in listOf("--encryption-key-file", "--codec", "--config", "--negotiate")) {
            assertTrue(proxy.arguments.none { it.startsWith(forbidden) }, "$forbidden means nothing in this mode")
        }
    }

    @Test
    fun theStreamModeHasNoExit() {
        val e = assertFailsWith<IllegalArgumentException> { CoreConfig.build(cups(), AppSettings(mode = ConnectionMode.Exit), paths) }
        assertTrue(e.message!!.contains("только как клиент"))
    }

    @Test
    fun profilesSavedBeforeTheModeExistedStillLoad() {
        val old = """{"id":"a","name":"Дом","transport":"vyandex","value":"https://disk.yandex.ru/i/x","secret":"","codec":"batched","session":false,"priority":100,"context":"","extras":[],"source":"Manual","createdAt":1}"""
        val p = Json { ignoreUnknownKeys = true }.decodeFromString(Profile.serializer(), old)
        assertFalse(p.stream)
        assertNull(p.phpNode)
        assertTrue(p.problems().isEmpty())
        // ... and a stream profile survives a save and load, token included.
        val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }
        val back = json.decodeFromString(Profile.serializer(), json.encodeToString(Profile.serializer(), cups()))
        assertEquals(cups(), back)
    }

    @Test
    fun everyCoreReasonHasWords() {
        // The lists mirror the core's codes (share.Code*, phphost.Code*); a code without words would show the fallback.
        for (code in PhpMessages.codes) {
            val text = PhpMessages.text(code, "x")
            assertNotNull(text)
            assertFalse(text.startsWith("Ошибка установки на хостинг"), "$code has no words of its own")
        }
        for (code in ShareLinkMessages.codes) {
            assertFalse(ShareLinkMessages.text(code, "x").startsWith("Не удалось обработать ссылку"), "$code has no words of its own")
        }
        assertTrue(ShareLinkMessages.text("stream_transport", "boards").contains("cups.online или Mail.ru"))
        assertTrue(PhpMessages.text("php_missing", "usleep,openssl").contains("usleep,openssl"))
        assertTrue(PhpMessages.text("never-heard-of-it", "", "detail").contains("detail"))
    }

    @Test
    fun hostingFormHelpers() {
        assertEquals("https://mysite.42web.io", PhpHosts.siteUrl(" MySite.42web.io/some/path?x=1 "))
        assertEquals("http://a.example:8080", PhpHosts.siteUrl("http://A.example:8080/"))
        assertNull(PhpHosts.siteUrl("localhost"))
        assertNull(PhpHosts.siteUrl("a b.c"))
        assertNull(PhpHosts.siteUrl("ftp://a.b"))
        assertEquals("ftpupload.net", PhpHosts.ftpHost(" ftp://ftpupload.net/ "))
        assertNull(PhpHosts.ftpHost("bad host"))
        assertEquals(room, PhpHosts.cupsRoom("0A1B2C3D-1111-2222-3333-444455556666"))
        assertEquals(room, PhpHosts.cupsRoom(room))
        assertNull(PhpHosts.cupsRoom("not a room"))
        assertNotNull(PhpHosts.presetFor("FTPUPLOAD.NET"))
        assertTrue(PhpHosts.ftpProblems("ftpupload.net", "21", "u", "p").isEmpty())
        assertEquals(2, PhpHosts.ftpProblems("", "0", "u", "p").size)
        assertNull(PhpMessages.security("tls"))
        assertTrue(PhpMessages.security("none")!!.contains("открытым текстом"))
    }
}
