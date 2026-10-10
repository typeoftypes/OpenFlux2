package io.openflux.desktop

import io.openflux.desktop.core.CliCoreLinks
import io.openflux.desktop.core.CoreBinary
import io.openflux.desktop.model.AppSettings
import io.openflux.desktop.model.CoreSource
import io.openflux.desktop.model.CoreShareLinkCodec
import io.openflux.desktop.model.ExtraTransport
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ProfileSource
import io.openflux.desktop.model.ShareConfig
import io.openflux.desktop.model.ShareLinkException
import io.openflux.desktop.model.ShareLinkMessages
import io.openflux.desktop.model.ShareTransport
import io.openflux.desktop.model.TransportType
import io.openflux.desktop.service.SettingsRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/**
 * The app against the real core (the one it ships, or OPENFLUX_CORE): what
 * it exports it imports back unchanged, a link mangled on the way still
 * reads, and every reason the core gives has the app's words. Skipped when
 * no core binary is around (a plain `:shared:jvmTest` without a build).
 */
class CoreLinksContractTest {
    private val core: File? = System.getenv("OPENFLUX_CORE")?.let(::File)?.takeIf { it.isFile }
        ?: File("..", "desktopApp/resources").walk().firstOrNull { f ->
            val os = System.getProperty("os.name").lowercase()
            val tag = when { os.contains("win") -> "windows"; os.contains("mac") -> "darwin"; else -> "linux" }
            f.isFile && f.name.startsWith("openflux-$tag-")
        }

    private fun codec(): CoreShareLinkCodec? {
        val file = core ?: return null.also { println("CoreLinksContractTest: no core binary, skipped") }
        val settings = object : SettingsRepository {
            override val settings = MutableStateFlow(AppSettings(coreSource = CoreSource.Custom, customCorePath = file.absolutePath))
            override fun update(transform: (AppSettings) -> AppSettings) = Unit
        }
        return CoreShareLinkCodec(CliCoreLinks(settings, CoreBinary()))
    }

    private val secret = "0123456789abcdef".repeat(4)

    @Test
    fun exportedProfilesImportBack() = runTest {
        val codec = codec() ?: return@runTest
        val profiles = listOf(
            Profile(
                id = "a", name = "Дом", transport = TransportType.YANDEX, value = "https://disk.yandex.ru/i/xyz",
                secret = secret, session = true, priority = 100,
                extras = listOf(ExtraTransport(TransportType.DIRECT, "1.2.3.4:8445", priority = 50)),
            ),
            Profile(id = "b", name = "Классика", transport = TransportType.MAILRU, value = "https://cloud.mail.ru/public/Ab/Cd", secret = secret),
            Profile(id = "c", name = "Без ключа", transport = TransportType.VYANDEX, value = "https://disk.yandex.ru/i/open"),
        )
        for (profile in profiles) {
            val link = codec.encode(profile.toShare().getOrThrow())
            val back = Profile.fromShare(codec.decode(link), profile.id, 0, ProfileSource.Qr)
            // The core names the context of a Session link by its rule.
            val context = if (profile.session) profile.value else ""
            assertEquals(profile.copy(context = context, source = ProfileSource.Qr), back, profile.name)
        }
    }

    /**
     * The iOS app keeps profiles its own way and hands the core what
     * ShareLink.encode builds (no codec, no context, documents at 100, the
     * direct channel at 50): the same profile must make the same link.
     */
    @Test
    fun sameLinksAsTheIosApp() = runTest {
        val codec = codec() ?: return@runTest
        val doc = "https://disk.yandex.ru/i/xyz"
        val cases = listOf(
            Profile(id = "a", name = "Дом", transport = TransportType.MAILRU, value = "https://cloud.mail.ru/public/Ab/Cd", secret = secret) to
                ShareConfig(name = "Дом", secret = secret, transports = listOf(ShareTransport("mailru", url = "https://cloud.mail.ru/public/Ab/Cd", priority = 100))),
            Profile(
                id = "b", name = "Нода", transport = TransportType.VYANDEX, value = doc, secret = secret, session = true,
                extras = listOf(ExtraTransport(TransportType.DIRECT, "203.0.113.7:9443", priority = 50)),
            ) to ShareConfig(
                name = "Нода", negotiate = true, secret = secret,
                transports = listOf(ShareTransport("vyandex", url = doc, priority = 100), ShareTransport("direct", dial = "203.0.113.7:9443", priority = 50)),
            ),
            Profile(id = "c", name = "Прямой", transport = TransportType.DIRECT, value = "203.0.113.7:9443", secret = secret, session = true) to
                ShareConfig(name = "Прямой", negotiate = true, secret = secret, transports = listOf(ShareTransport("direct", dial = "203.0.113.7:9443"))),
        )
        for ((profile, ios) in cases) {
            assertEquals(codec.encode(ios), codec.encode(profile.toShare().getOrThrow()), profile.name)
        }
    }

    @Test
    fun linksMangledOnTheWayStillRead() = runTest {
        val codec = codec() ?: return@runTest
        val config = ShareConfig(
            name = "node", negotiate = true, secret = secret,
            transports = listOf(ShareTransport("vyandex", url = "https://disk.yandex.ru/i/A", priority = 100), ShareTransport("direct", dial = "203.0.113.7:9443", priority = 50)),
        )
        val link = codec.encode(config)
        val body = link.removePrefix("openflux://v1/")
        val raw = java.util.Base64.getUrlDecoder().decode(body)
        val want = codec.decode(link)
        for (mangled in listOf(
            "  $link\n",
            link.chunked(80).joinToString("\n"),
            link.chunked(76).joinToString("\r\n"),
            link.substring(0, 60) + " " + link.substring(60),
            link.substring(0, 60) + " " + link.substring(60),
            link.substring(0, 60) + "​" + link.substring(60),
            "openflux://v1/" + java.util.Base64.getEncoder().encodeToString(raw),
        )) {
            assertEquals(want, codec.decode(mangled), mangled)
        }
    }

    @Test
    fun theCoresReasonsAreWorded() = runTest {
        val codec = codec() ?: return@runTest
        val fallback = ShareLinkMessages.text("", "", "")
        suspend fun reason(block: suspend () -> Unit): ShareLinkException = try {
            block(); error("accepted")
        } catch (e: ShareLinkException) {
            e
        }
        val cases = mapOf(
            "not_link" to reason { codec.decode("https://example.com") },
            "unsupported_version" to reason { codec.decode("openflux://v2/abc") },
            "damaged" to reason { codec.decode("openflux://v1/!!!") },
            "no_transports" to reason { codec.encode(ShareConfig()) },
            "several_need_session" to reason { codec.encode(ShareConfig(transports = listOf(ShareTransport("yandex", url = "https://a"), ShareTransport("mailru", url = "https://b")))) },
            "short_secret" to reason { codec.encode(ShareConfig(secret = "short", transports = listOf(ShareTransport("yandex", url = "https://a")))) },
            "not_shareable" to reason { codec.encode(ShareConfig(transports = listOf(ShareTransport("oneme")))) },
            "unknown_transport" to reason { codec.encode(ShareConfig(transports = listOf(ShareTransport("pigeon")))) },
        )
        for ((code, e) in cases) {
            assertEquals(code, e.code)
            assertTrue(code in ShareLinkMessages.codes, code)
            assertNotEquals(fallback, e.message, code)
        }
        assertTrue("16" in cases.getValue("short_secret").message!!)
        assertFailsWith<ShareLinkException> { codec.decode("OPENFLUX://V1/ABC") }.also { assertEquals("case_changed", it.code) }
    }
}
