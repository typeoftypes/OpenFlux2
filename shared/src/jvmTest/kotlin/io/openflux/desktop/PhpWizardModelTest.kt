package io.openflux.desktop

import io.openflux.desktop.model.AppSettings
import io.openflux.desktop.model.CaptchaPrompt
import io.openflux.desktop.model.ConnectionMode
import io.openflux.desktop.model.ConnectionState
import io.openflux.desktop.model.ExitAddress
import io.openflux.desktop.model.FtpTarget
import io.openflux.desktop.model.LogLevel
import io.openflux.desktop.model.LogLine
import io.openflux.desktop.model.PhpHostingException
import io.openflux.desktop.model.PhpHosts
import io.openflux.desktop.model.PhpMessages
import io.openflux.desktop.model.PhpInstalled
import io.openflux.desktop.model.PhpNodeState
import io.openflux.desktop.model.PhpProbe
import io.openflux.desktop.model.PhpProgress
import io.openflux.desktop.model.PhpRoom
import io.openflux.desktop.model.PhpStatus
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ProfileSource
import io.openflux.desktop.model.TrafficStats
import io.openflux.desktop.model.TransportType
import io.openflux.desktop.service.AppContainer
import io.openflux.desktop.service.ConnectionService
import io.openflux.desktop.service.PhpHostingService
import io.openflux.desktop.service.PlatformServices
import io.openflux.desktop.service.ProfileRepository
import io.openflux.desktop.service.SettingsRepository
import io.openflux.desktop.ui.BrowserPage
import io.openflux.desktop.ui.node.PhpStep
import io.openflux.desktop.ui.node.PhpWizardModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class PhpWizardModelTest {
    private val room = "https://interview.cups.online/live-coding/?room=0a1b2c3d-1111-2222-3333-444455556666"

    private fun PhpWizardModel.fillHosting() {
        ftpHost = "ftpupload.net"
        ftpUser = "if0_123"
        ftpPassword = "ftp-secret"
        siteInput = "mysite.42web.io"
    }

    @Test
    fun wholeWizardSavesAVerifiedStreamProfile() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting()
        advanceUntilIdle()
        assertNull(wizard.error)
        assertEquals(PhpStep.Channel, wizard.step)
        assertEquals("Свой хостинг · ftpupload.net", wizard.name)
        assertEquals("https://mysite.42web.io", wizard.siteUrl)
        assertEquals(listOf("if0_123@ftpupload.net:21"), env.php.probed)

        wizard.prepareChannel()
        advanceUntilIdle()
        assertEquals(PhpStep.Install, wizard.step)
        assertEquals(room, wizard.target)
        assertEquals(1, env.php.roomsMade)

        wizard.install()
        advanceUntilIdle()
        assertEquals(PhpStep.Done, wizard.step, wizard.verifyFailed ?: wizard.error ?: "")
        assertEquals(listOf("deploy", "check", "start chain=true"), env.php.steps)
        assertTrue(wizard.unsaved)
        assertEquals("203.0.113.44", wizard.verifiedIp)
        assertTrue(wizard.shareLink.startsWith("openflux://"))

        val saved = wizard.save()!!
        assertTrue(saved.stream)
        assertEquals(TransportType.CUPSONLINE, saved.transport)
        assertEquals(room, saved.value)
        assertEquals(ProfileSource.Node, saved.source)
        assertEquals("https://mysite.42web.io", saved.phpNode?.siteUrl)
        assertEquals("tok123", saved.phpNode?.token)
        assertEquals(listOf(saved), env.profiles.profiles.value)
        assertEquals(saved.id, env.settings.settings.value.selectedProfileId)
        assertFalse(wizard.unsaved)
        assertTrue(saved.problems().isEmpty())

        wizard.close()
        assertEquals("", wizard.ftpPassword)
        assertTrue(env.php.closed)
        // The saved profile keeps its connection.
        assertTrue(env.connection.state.value is ConnectionState.Connected)
    }

    @Test
    fun theFormIsCheckedBeforeAnythingIsSent() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.probeHosting()
        assertEquals("Укажите адрес FTP-сервера, например ftpupload.net", wizard.error)
        wizard.ftpHost = "ftpupload.net"
        wizard.ftpUser = "u"
        wizard.probeHosting()
        assertEquals("Укажите пароль FTP", wizard.error)
        wizard.ftpPassword = "p"
        wizard.siteInput = "not a site"
        wizard.probeHosting()
        assertTrue(wizard.error!!.startsWith("Укажите адрес сайта"))
        wizard.ftpPort = "99999"
        wizard.siteInput = "a.b.c"
        wizard.probeHosting()
        assertTrue(wizard.error!!.contains("Порт FTP"))
        advanceUntilIdle()
        assertTrue(env.php.probed.isEmpty(), "nothing must reach the core from a bad form")
        assertEquals(PhpStep.Hosting, wizard.step)
    }

    @Test
    fun anUnclearWebFolderIsSettledByThePicking() = runTest {
        val env = Env()
        env.php.probeFails = PhpHostingException("ftp_no_webroot", "alpha,beta")
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting()
        advanceUntilIdle()
        assertEquals(PhpStep.Hosting, wizard.step)
        assertEquals(listOf("alpha", "beta"), wizard.folderChoices)
        assertNotNull(wizard.error)

        env.php.probeFails = null
        wizard.chooseFolder("beta")
        advanceUntilIdle()
        assertEquals(PhpStep.Channel, wizard.step)
        assertEquals("beta", env.php.lastFtp?.dir)
        assertTrue(wizard.folderChoices.isEmpty())
    }

    @Test
    fun mailruNeedsAPublicDocumentLink() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting()
        advanceUntilIdle()
        wizard.carrier = TransportType.MAILRU
        wizard.mailruInput = "https://example.com/doc"
        wizard.prepareChannel()
        assertTrue(wizard.error!!.contains("Mail.ru"))
        assertEquals(PhpStep.Channel, wizard.step)
        wizard.mailruInput = " https://cloud.mail.ru/public/Vuri/d5nuZ5aQp?weblink=x#y "
        wizard.prepareChannel()
        assertNull(wizard.error)
        assertEquals("https://cloud.mail.ru/public/Vuri/d5nuZ5aQp", wizard.target)
        assertEquals(PhpStep.Install, wizard.step)
        assertEquals(0, env.php.roomsMade, "a Mail.ru channel needs no cups room")
    }

    @Test
    fun aNewDomainIsGivenTimeToStartAnswering() = runTest {
        val env = Env()
        env.php.checkFailures = mutableListOf("site_unreachable", "site_unreachable", "site_not_phpbox")
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting(); advanceUntilIdle()
        wizard.prepareChannel(); advanceUntilIdle()
        wizard.install(); advanceUntilIdle()
        assertEquals(PhpStep.Done, wizard.step, wizard.error ?: wizard.verifyFailed ?: "")
        assertEquals(4, env.php.steps.count { it == "check" }, "three failures, then the fourth try works")
    }

    @Test
    fun aHostWithoutTheFunctionsStopsAtOnce() = runTest {
        val env = Env()
        env.php.checkFailures = mutableListOf("php_missing")
        env.php.checkParam = "usleep,openssl"
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting(); advanceUntilIdle()
        wizard.prepareChannel(); advanceUntilIdle()
        wizard.install(); advanceUntilIdle()
        assertEquals(PhpStep.Install, wizard.step)
        assertTrue(wizard.error!!.contains("usleep,openssl"), wizard.error)
        assertEquals(1, env.php.steps.count { it == "check" }, "a final reason is not retried")
        assertFalse(env.php.steps.any { it.startsWith("start") }, "the node must not be started on a host that cannot run it")
        assertTrue(wizard.unsaved, "files are on the host: leaving must offer to remove or keep them")
    }

    @Test
    fun aFailedVerificationCanBeRetriedOrSkipped() = runTest {
        val env = Env(exitIp = null)
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting(); advanceUntilIdle()
        wizard.prepareChannel(); advanceUntilIdle()
        wizard.install(); advanceUntilIdle()
        assertEquals(PhpStep.Verify, wizard.step)
        assertNotNull(wizard.verifyFailed)
        assertEquals(ConnectionState.Idle, env.connection.state.value, "the test connection of a failed node must not stay up")

        env.connection.exitIp = "198.51.100.7"
        wizard.retryVerify(); advanceUntilIdle()
        assertEquals(PhpStep.Done, wizard.step, wizard.verifyFailed ?: "")
        assertEquals("198.51.100.7", wizard.verifiedIp)
    }

    @Test
    fun theNodeCanBeStoppedAndTheFilesRemoved() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting(); advanceUntilIdle()
        wizard.prepareChannel(); advanceUntilIdle()
        wizard.install(); advanceUntilIdle()
        wizard.stopNode(); advanceUntilIdle()
        assertFalse(wizard.nodeRunning)
        assertTrue(env.php.steps.contains("stop"))
        wizard.removeFromHosting(); advanceUntilIdle()
        assertTrue(env.php.steps.contains("remove"))
        assertNull(wizard.installed)
        assertFalse(wizard.unsaved, "nothing is left on the host to lose")
    }

    // ---- a node already on the hosting ----

    @Test
    fun aNodeUploadedByHandIsAddedFromItsPageAddress() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.existing = true
        wizard.onExistingAddress(
            "https://mysite.42web.io/mailruexit.php?k=handkey99&url=https%3A%2F%2Fcloud.mail.ru%2Fpublic%2FVuri%2Fd5nuZ5aQp",
        )
        assertEquals("handkey99", wizard.tokenInput, "the key comes from the page address")
        assertEquals(TransportType.MAILRU, wizard.carrier)
        assertEquals("https://cloud.mail.ru/public/Vuri/d5nuZ5aQp", wizard.mailruInput)

        wizard.useExisting()
        assertNull(wizard.error)
        assertEquals(PhpStep.Channel, wizard.step)
        assertEquals("https://mysite.42web.io", wizard.siteUrl)
        assertEquals("Свой хостинг · mysite.42web.io", wizard.name)

        wizard.prepareChannel()
        advanceUntilIdle()
        assertEquals(PhpStep.Install, wizard.step)
        assertEquals("https://cloud.mail.ru/public/Vuri/d5nuZ5aQp", wizard.target)

        wizard.install()
        advanceUntilIdle()
        assertEquals(PhpStep.Done, wizard.step, wizard.verifyFailed ?: wizard.error ?: "")
        assertEquals(listOf("check", "start chain=true"), env.php.steps, "no FTP and no upload for a node already there")
        assertTrue(env.php.probed.isEmpty())
        assertEquals(listOf("handkey99", "handkey99"), env.php.tokens)
        assertFalse(wizard.canRemove, "the wizard did not put the files there, so it offers no removal")
        assertEquals("hand…ey99", wizard.maskedToken)

        val saved = wizard.save()!!
        assertEquals("https://mysite.42web.io", saved.phpNode?.siteUrl)
        assertEquals("handkey99", saved.phpNode?.token)
        assertEquals(TransportType.MAILRU, saved.transport)
        wizard.close()
        assertEquals("", wizard.tokenInput)
    }

    @Test
    fun aCupsNodeKeepsTheRoomItsAddressNames() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.existing = true
        wizard.onExistingAddress("https://mysite.42web.io/cupsexit.php?k=abc12345&room=0A1B2C3D-1111-2222-3333-444455556666")
        assertEquals(TransportType.CUPSONLINE, wizard.carrier)
        assertEquals(room, wizard.knownRoom)
        wizard.useExisting()
        wizard.prepareChannel()
        advanceUntilIdle()
        assertEquals(room, wizard.target)
        assertEquals(0, env.php.roomsMade, "the node already waits in its room: no new one")
    }

    @Test
    fun aWrongKeyIsWordedForTheUserWhoTypedIt() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.existing = true
        wizard.onExistingAddress("mysite.42web.io")
        wizard.useExisting()
        assertEquals("Укажите ключ доступа ноды", wizard.error)
        assertEquals(PhpStep.Hosting, wizard.step)

        wizard.tokenInput = "wrong"
        wizard.useExisting()
        wizard.prepareChannel()
        advanceUntilIdle()
        env.php.checkFailures += "site_token"
        wizard.install()
        advanceUntilIdle()
        assertEquals(PhpMessages.text("site_token_given"), wizard.error)
        assertTrue(wizard.error!!.contains("k="))
        assertEquals(PhpStep.Install, wizard.step)
        assertNull(wizard.installed, "nothing is kept from a key the node refused")
        assertEquals(listOf("check"), env.php.steps)
    }

    @Test
    fun aNodePageAddressIsReadApart() {
        val m = PhpHosts.nodeAddress("https://site.example.org/sub/mailruexit.php?k=K1&url=https%3A%2F%2Fcloud.mail.ru%2Fpublic%2Fa%2Fb")!!
        assertEquals("https://site.example.org", m.site)
        assertEquals("K1", m.token)
        assertEquals(TransportType.MAILRU, m.carrier)
        assertEquals("https://cloud.mail.ru/public/a/b", m.target)
        val plain = PhpHosts.nodeAddress("site.example.org")!!
        assertEquals("https://site.example.org", plain.site)
        assertEquals("", plain.token)
        assertNull(plain.carrier)
        assertNull(PhpHosts.nodeAddress("not a site"))
        assertEquals("••••", PhpHosts.maskToken("abcd"))
        assertEquals("Ключ доступа — одна строка без пробелов", PhpHosts.tokenProblem("a b"))
    }

    // ---- a key of one's own, the generation, the panel ----

    @Test
    fun aChosenKeyGoesToTheInstallAndABadOneStopsTheForm() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.chosenToken = "bad key"
        wizard.probeHosting()
        advanceUntilIdle()
        assertEquals(PhpMessages.TOKEN_SHAPE, wizard.error)
        assertTrue(env.php.probed.isEmpty(), "nothing reaches the core with a bad key")

        wizard.chosenToken = "My-own_key-2026"
        wizard.probeHosting()
        advanceUntilIdle()
        wizard.prepareChannel()
        advanceUntilIdle()
        wizard.install()
        advanceUntilIdle()
        assertEquals("My-own_key-2026", env.php.deployToken)
    }

    @Test
    fun theDoneStepShowsTheGenerationAndOpensThePanel() = runTest {
        val env = Env()
        val wizard = PhpWizardModel(env.container, this)
        wizard.fillHosting()
        wizard.probeHosting(); advanceUntilIdle()
        wizard.prepareChannel(); advanceUntilIdle()
        wizard.install(); advanceUntilIdle()
        assertEquals("", env.php.deployToken, "no chosen key: the core keeps the old one or makes one")
        assertEquals("работает · поколение 3 · смена через 30 с", PhpMessages.nodeStatus(wizard.nodeState!!))

        wizard.refreshNode(); advanceUntilIdle()
        assertEquals(4, wizard.nodeState?.state?.gen)
        assertEquals("работает · поколение 4 · смена через 12 с · предыдущее дорабатывает соединения", PhpMessages.nodeStatus(wizard.nodeState!!))

        wizard.openPanel(); advanceUntilIdle()
        assertEquals(listOf("https://mysite.42web.io|tok123|cupsonline"), env.php.pages)
        assertEquals(listOf("https://mysite.42web.io/cupsexit.php?k=tok123&auto=0"), env.platform.opened)
        assertEquals("остановлена", PhpMessages.nodeStatus(io.openflux.desktop.model.PhpNodeState()))
    }

    // ---- fakes ----

    private class FakePhp : PhpHostingService() {
        val steps = mutableListOf<String>()
        val probed = mutableListOf<String>()
        var roomsMade = 0
        var probeFails: PhpHostingException? = null
        var lastFtp: FtpTarget? = null
        var checkFailures = mutableListOf<String>()
        var checkParam = ""
        var closed = false
        val tokens = mutableListOf<String>()      // the key each call to the site carried

        override suspend fun probe(ftp: FtpTarget): PhpProbe {
            lastFtp = ftp
            probeFails?.let { throw it }
            probed += "${ftp.user}@${ftp.host}:${ftp.port}"
            return PhpProbe(security = "none", dir = "htdocs", writable = true)
        }

        var deployToken = "?"
        val pages = mutableListOf<String>()

        override suspend fun deploy(ftp: FtpTarget, token: String, onProgress: (PhpProgress) -> Unit): PhpInstalled {
            steps += "deploy"
            deployToken = token
            onProgress(PhpProgress("upload", "lib/mux.php", 1, 2, 10, 100))
            return PhpInstalled(dir = "htdocs", token = "tok123", files = 12, bytes = 100, security = "none")
        }

        override suspend fun remove(ftp: FtpTarget) { steps += "remove" }

        override suspend fun check(site: String, token: String, carrier: String): PhpStatus {
            steps += "check"
            tokens += token
            if (checkFailures.isNotEmpty()) throw PhpHostingException(checkFailures.removeAt(0), checkParam)
            return PhpStatus(version = "0.4", carrier = carrier, php = "8.4")
        }

        override suspend fun start(site: String, token: String, carrier: String, target: String, chain: Boolean, quiet: Boolean): PhpNodeState {
            steps += "start chain=$chain"
            tokens += token
            return PhpNodeState(running = true, chain = chain, nextIn = 30, state = io.openflux.desktop.model.PhpNodeInfo(gen = 3, phase = "serving"))
        }

        override suspend fun stop(site: String, token: String, carrier: String, target: String) { steps += "stop" }
        override suspend fun node(site: String, token: String, carrier: String, target: String): PhpNodeState {
            steps += "node"
            return PhpNodeState(running = true, chain = true, draining = 1, nextIn = 12, state = io.openflux.desktop.model.PhpNodeInfo(gen = 4, phase = "serving"))
        }

        override suspend fun page(site: String, token: String, carrier: String, target: String): String {
            pages += "$site|$token|$carrier"
            return "$site/${if (carrier == "mailru") "mailruexit" else "cupsexit"}.php?k=$token&auto=0"
        }

        override suspend fun newRoom(): PhpRoom {
            roomsMade++
            return PhpRoom("0a1b2c3d-1111-2222-3333-444455556666", "https://interview.cups.online/live-coding/?room=0a1b2c3d-1111-2222-3333-444455556666")
        }

        override suspend fun link(name: String, carrier: String, target: String) = "openflux://v1/test-$carrier"
        override fun close() { closed = true }
        override val logs: StateFlow<List<LogLine>> = MutableStateFlow(emptyList())
        override fun note(text: String, level: LogLevel) = Unit
    }

    private class Env(exitIp: String? = "203.0.113.44") {
        val settings = FakeSettings()
        val profiles = FakeProfiles()
        val platform = FakePlatform()
        val connection = FakeConnection(exitIp, platform)
        val php = FakePhp()
        val container = AppContainer(
            profiles, settings, connection, platform, FakeShareLinkCodec(),
            object : io.openflux.desktop.service.NodeWizardService by unsupported() {},
            php,
        )
    }

    private class FakeConnection(var exitIp: String?, private val platform: FakePlatform) : ConnectionService {
        override val state = MutableStateFlow<ConnectionState>(ConnectionState.Idle)
        override val traffic = MutableStateFlow(TrafficStats())
        override val exitAddress = MutableStateFlow<ExitAddress>(ExitAddress.Unknown)
        override val logs: StateFlow<List<LogLine>> = MutableStateFlow(emptyList())
        override val captcha: StateFlow<CaptchaPrompt?> = MutableStateFlow(null)
        override val exitShareLink: StateFlow<String?> = MutableStateFlow(null)
        override val socksAddress: StateFlow<String?> = MutableStateFlow(null)
        override val captchaPage: StateFlow<BrowserPage?> = MutableStateFlow(null)

        override fun connect(profile: Profile) {
            state.value = ConnectionState.Connected(profile, ConnectionMode.Client, 0)
            exitAddress.value = check()
        }

        override fun disconnect() {
            state.value = ConnectionState.Idle
            exitAddress.value = ExitAddress.Unknown
        }

        override fun refreshExitAddress() { exitAddress.value = check() }

        private fun check(): ExitAddress {
            platform.skipped += 60_000 // each failed look costs a minute of the verification's budget
            return exitIp?.let { ExitAddress.Known(it) } ?: ExitAddress.Unavailable("Tunnel failed, got: 502")
        }

        override fun clearLogs() = Unit
        override fun openCaptcha() = Unit
        override fun submitCaptcha() = Unit
        override fun dismissCaptcha() = Unit
        override fun shutdown() = Unit
    }

    private class FakeSettings : SettingsRepository {
        override val settings = MutableStateFlow(AppSettings())
        override fun update(transform: (AppSettings) -> AppSettings) { settings.value = transform(settings.value) }
    }

    private class FakeProfiles : ProfileRepository {
        override val profiles = MutableStateFlow<List<Profile>>(emptyList())
        private var next = 0
        override fun upsert(profile: Profile) { profiles.value = profiles.value.filterNot { it.id == profile.id } + profile }
        override fun delete(id: String) { profiles.value = profiles.value.filterNot { it.id == id } }
        override fun newId() = "p${next++}"
    }

    private class FakePlatform : PlatformServices {
        var clipboard: String? = null
        var skipped = 0L
        override val appVersion = "test"
        override val coreVersion = "test"
        override val clientRepo = "test"
        override val systemProxySupported = false
        override val fullTunnelSupported = false
        override val elevated = false
        override fun restartElevated() = false
        override fun clipboardText(): String? = clipboard
        override fun setClipboardText(text: String) { clipboard = text }
        override fun qrFromClipboardImage(): String? = null
        override fun qrFromFile(path: String): String? = null
        override suspend fun pickFile(title: String, extensions: List<String>): String? = null
        override fun readTextFile(path: String, maxBytes: Int): String? = null
        override fun qrMatrix(text: String): List<BooleanArray> = emptyList()
        val opened = mutableListOf<String>()
        override fun openUrl(url: String) { opened += url }
        override fun newSecret() = "00".repeat(32)
        override fun now() = System.currentTimeMillis() + skipped
        override suspend fun latestRelease(): String? = null
    }
}

private fun unsupported(): io.openflux.desktop.service.NodeWizardService = java.lang.reflect.Proxy.newProxyInstance(
    io.openflux.desktop.service.NodeWizardService::class.java.classLoader,
    arrayOf(io.openflux.desktop.service.NodeWizardService::class.java),
) { _, method, _ ->
    when (method.name) {
        "getLogs" -> MutableStateFlow<List<LogLine>>(emptyList())
        else -> Unit
    }
} as io.openflux.desktop.service.NodeWizardService
