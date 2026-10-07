package io.openflux.desktop

import io.openflux.desktop.core.CliCoreLinks
import io.openflux.desktop.core.CoreBinary
import io.openflux.desktop.model.AppSettings
import io.openflux.desktop.model.CoreShareLinkCodec
import io.openflux.desktop.model.CoreSource
import io.openflux.desktop.model.PhpHostingException
import io.openflux.desktop.model.PhpMessages
import io.openflux.desktop.model.PhpNodeRef
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ProfileSource
import io.openflux.desktop.model.ShareConfig
import io.openflux.desktop.model.ShareLinkException
import io.openflux.desktop.model.ShareLinkMessages
import io.openflux.desktop.model.ShareTransport
import io.openflux.desktop.model.TransportType
import io.openflux.desktop.node.CorePhpTransport
import io.openflux.desktop.service.PhpHostingService
import io.openflux.desktop.service.SettingsRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/**
 * The mode without a server against the real core (the one the app ships, or
 * OPENFLUX_CORE): the link it makes and reads, the reasons it gives, and the
 * hosting steps over its `--node-wizard` protocol. Skipped when there is no
 * core binary around.
 */
class StreamContractTest {
    private val core: File? = System.getenv("OPENFLUX_CORE")?.let(::File)?.takeIf { it.isFile }
        ?: File("..", "desktopApp/resources").walk().firstOrNull { f ->
            val os = System.getProperty("os.name").lowercase()
            val tag = when { os.contains("win") -> "windows"; os.contains("mac") -> "darwin"; else -> "linux" }
            f.isFile && f.name.startsWith("openflux-$tag-")
        }

    private fun settings(file: File) = object : SettingsRepository {
        override val settings = MutableStateFlow(AppSettings(coreSource = CoreSource.Custom, customCorePath = file.absolutePath))
        override fun update(transform: (AppSettings) -> AppSettings) = Unit
    }

    private fun codec(): CoreShareLinkCodec? {
        val file = core ?: return null.also { println("StreamContractTest: no core binary, skipped") }
        return CoreShareLinkCodec(CliCoreLinks(settings(file), CoreBinary()))
    }

    private val room = "https://interview.cups.online/live-coding/?room=0a1b2c3d-1111-2222-3333-444455556666"

    @Test
    fun aStreamProfileIsExportedAndImportedByTheCore() = runTest {
        val codec = codec() ?: return@runTest
        for ((transport, value) in listOf(TransportType.CUPSONLINE to room, TransportType.MAILRU to "https://cloud.mail.ru/public/Vuri/d5nuZ5aQp")) {
            val profile = Profile(id = "s", name = "Без сервера", transport = transport, value = value, stream = true, phpNode = PhpNodeRef("https://x.example", "tok"))
            val link = codec.encode(profile.toShare().getOrThrow())
            val config = codec.decode(link)
            assertEquals("stream", config.mode)
            val back = Profile.fromShare(config, "s", 0, ProfileSource.Qr)
            assertEquals(profile.copy(source = ProfileSource.Qr, phpNode = null), back, "$transport")
            assertFalse(link.contains("tok"), "the node's token must not be in a link")
        }
    }

    @Test
    fun theCoresStreamReasonsAreWorded() = runTest {
        val codec = codec() ?: return@runTest
        val fallback = ShareLinkMessages.text("", "", "")
        suspend fun reason(block: suspend () -> Unit): ShareLinkException = try {
            block(); error("accepted")
        } catch (e: ShareLinkException) {
            e
        }
        val cases = mapOf(
            "unknown_mode" to reason { codec.encode(ShareConfig(mode = "teleport", transports = listOf(ShareTransport("mailru", url = "https://a")))) },
            "stream_transport" to reason { codec.encode(ShareConfig(mode = "stream", transports = listOf(ShareTransport("boards", url = "https://a")))) },
            "stream_one_transport" to reason { codec.encode(ShareConfig(mode = "stream", transports = listOf(ShareTransport("mailru", url = "https://a"), ShareTransport("cupsonline")))) },
            "stream_plain_only" to reason { codec.encode(ShareConfig(mode = "stream", secret = "0123456789abcdef", transports = listOf(ShareTransport("mailru", url = "https://a")))) },
        )
        for ((code, e) in cases) {
            assertEquals(code, e.code)
            assertTrue(code in ShareLinkMessages.codes, code)
            assertNotEquals(fallback, e.message, code)
        }
    }

    @Test
    fun theHostingStepsSpeakToTheCore() = runTest {
        val file = core ?: return@runTest println("StreamContractTest: no core binary, skipped")
        val transport = CorePhpTransport(settings(file), CoreBinary())
        val service = PhpHostingService(transport)
        try {
            // A link for the node, made by the core.
            val link = service.link("Free", "mailru", "https://cloud.mail.ru/public/Vuri/d5nuZ5aQp")
            assertTrue(link.startsWith("openflux://v1/"))
            // Reasons come back as codes the app words.
            val unreachable = assertFailsWith<PhpHostingException> {
                service.probe(io.openflux.desktop.model.FtpTarget(host = "127.0.0.1", port = 1, user = "u", password = "p", tls = "none"))
            }
            assertEquals("ftp_connect", unreachable.code)
            assertTrue(unreachable.code in PhpMessages.codes)
            assertNotEquals(PhpMessages.text(""), unreachable.message)
            val noSite = assertFailsWith<PhpHostingException> { service.check("http://127.0.0.1:1", "t", "mailru") }
            assertEquals("site_unreachable", noSite.code)
            val badCarrier = assertFailsWith<PhpHostingException> { service.link("x", "boards", "https://a") }
            assertEquals("stream_transport", badCarrier.code, "a code of the link format comes through the hosting protocol too")
        } finally {
            service.close()
        }
    }
}
