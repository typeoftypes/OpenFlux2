package io.openflux.desktop

import io.openflux.desktop.model.AppSettings
import io.openflux.desktop.model.CaptchaPrompt
import io.openflux.desktop.model.ConnectionMode
import io.openflux.desktop.model.ConnectionState
import io.openflux.desktop.model.ExitAddress
import io.openflux.desktop.model.KnownServer
import io.openflux.desktop.model.LogLevel
import io.openflux.desktop.model.LogLine
import io.openflux.desktop.model.NewChannel
import io.openflux.desktop.model.NodePlan
import io.openflux.desktop.model.NodeTransport
import io.openflux.desktop.model.NodeTransports
import io.openflux.desktop.model.NodeServers
import io.openflux.desktop.model.NodeWizardException
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ProfileSource
import io.openflux.desktop.model.ServerProbe
import io.openflux.desktop.model.ShareConfig
import io.openflux.desktop.model.ShareTransport
import io.openflux.desktop.model.SshTarget
import io.openflux.desktop.model.TrafficStats
import io.openflux.desktop.model.TransportType
import io.openflux.desktop.service.AppContainer
import io.openflux.desktop.service.ConnectionService
import io.openflux.desktop.service.NodeWizardService
import io.openflux.desktop.service.PlatformServices
import io.openflux.desktop.service.ProfileRepository
import io.openflux.desktop.service.SettingsRepository
import io.openflux.desktop.model.NodeDocuments
import io.openflux.desktop.ui.BrowserPage
import io.openflux.desktop.ui.node.NodeWizardModel
import io.openflux.desktop.ui.node.WizardStep
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class NodeWizardModelTest {
    private val docUrl = "https://disk.yandex.ru/edit/d/abcdefghijklmnopqrstuvwxyz"
    private val serverIp = "203.0.113.10"

    @Test
    fun wholeWizardSavesAVerifiedProfile() = runTest {
        val env = Env()
        val wizard = NodeWizardModel(env.container, this)
        wizard.host = serverIp
        wizard.password = "ssh-pass"
        wizard.connect()
        advanceUntilIdle()
        // A new server: its key must be confirmed before anything else.
        assertEquals("SHA256:new", wizard.hostKeyPrompt?.fingerprint)
        assertEquals(WizardStep.Server, wizard.step)
        wizard.trustHostKey()
        advanceUntilIdle()
        assertEquals("SHA256:new", env.settings.settings.value.knownHostKeys["$serverIp:22"])
        assertEquals(listOf(KnownServer(serverIp, 22, "root")), env.settings.settings.value.knownServers)
        assertEquals(WizardStep.Document, wizard.step)
        assertEquals("Нода $serverIp", wizard.name)

        wizard.documentInput = "$docUrl?sk=123"
        wizard.checkDocument()
        advanceUntilIdle()
        assertNull(wizard.error)
        assertEquals(WizardStep.Plan, wizard.step)
        assertEquals(docUrl, wizard.documentUrl)
        // sudo with a password: the SSH password is the likely one.
        assertTrue(wizard.needsSudoPassword)
        assertEquals("ssh-pass", wizard.sudoPassword)

        wizard.install()
        advanceUntilIdle()
        assertEquals(WizardStep.Done, wizard.step, wizard.verifyFailed ?: wizard.error ?: "")
        assertEquals(listOf("of-test12", "vyandex=$docUrl", "31337", "autoUpdate=true", "ssh-pass"), env.node.applied)
        assertEquals(serverIp, wizard.verifiedIp)
        assertTrue(wizard.primaryUp)
        assertTrue(wizard.unsaved)

        val saved = wizard.save()!!
        assertEquals(ProfileSource.Node, saved.source)
        assertTrue(saved.session)
        assertEquals(listOf(TransportType.VYANDEX, TransportType.DIRECT), saved.carriers.map { it.type })
        assertEquals("$serverIp:31337", saved.carriers[1].value)
        assertEquals(listOf(saved), env.profiles.profiles.value)
        assertFalse(wizard.unsaved)

        wizard.close()
        assertTrue(env.node.closed)
        // The saved profile keeps its connection.
        assertTrue(env.connection.state.value is ConnectionState.Connected)
        assertEquals("", wizard.password)
    }

    @Test
    fun wrongExitAddressFailsVerification() = runTest {
        val env = Env(exitIp = "198.51.100.1")
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        assertNull(wizard.hostKeyPrompt)
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        wizard.install()
        advanceUntilIdle()
        assertEquals(WizardStep.Verify, wizard.step)
        assertTrue(wizard.verifyFailed!!.contains("198.51.100.1"))
        // The test connection of a failed channel does not stay up.
        assertEquals(ConnectionState.Idle, env.connection.state.value)

        wizard.close()
        assertTrue(env.node.closed)
    }

    @Test
    fun verificationKeepsTryingWhileTheCarrierComesUp() = runTest {
        val env = Env()
        env.connection.failingChecks = 5
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        wizard.install()
        advanceUntilIdle()
        assertEquals(WizardStep.Done, wizard.step, wizard.verifyFailed ?: "")
        assertEquals(serverIp, wizard.verifiedIp)
        assertEquals(6, env.connection.checks)
    }

    @Test
    fun wrongSudoPasswordStaysOnPlan() = runTest {
        val env = Env(sudoFails = true)
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        wizard.install()
        advanceUntilIdle()
        assertEquals(WizardStep.Plan, wizard.step)
        assertEquals("sudo не принял пароль", wizard.error)
        assertFalse(wizard.installed)
    }

    @Test
    fun rejectsLinksThatAreNotYandexDocuments() = runTest {
        val env = Env()
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.documentInput = "https://example.com/edit/d/abcdefghijklmnopqrstuvwxyz"
        wizard.checkDocument()
        advanceUntilIdle()
        assertEquals(WizardStep.Document, wizard.step)
        assertTrue(wizard.error!!.startsWith("Нужна ссылка"))
    }

    @Test
    fun droppedConnectionIsOpenedAgainForThePlan() = runTest {
        val env = Env()
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        assertEquals(1, env.node.connects)
        // SSH dropped while the user was looking for the document link.
        env.node.dropped = true
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        assertNull(wizard.error)
        assertEquals(WizardStep.Plan, wizard.step)
        assertEquals(2, env.node.connects)
        assertEquals(1, env.node.plannedTransports.size)
    }

    @Test
    fun quietConnectionIsOpenedAgainBeforeInstall() = runTest {
        val env = Env()
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        assertEquals(1, env.node.connects)
        // The user reads the plan for a while: install must not reuse a dead connection.
        env.platform.skipped += 5 * 60_000L
        env.node.dropped = true
        wizard.install()
        advanceUntilIdle()
        assertEquals(2, env.node.connects)
        assertTrue(wizard.installed)
    }

    @Test
    fun scriptErrorsAreNotRetriedForever() = runTest {
        val env = Env()
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        env.node.planError = "порт 31337 занят"
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        assertEquals("порт 31337 занят", wizard.error)
        assertEquals(WizardStep.Document, wizard.step)
        assertEquals(2, env.node.connects)
    }

    @Test
    fun severalTransportsWithoutYandex() = runTest {
        val env = Env()
        env.connection.active = "mailru"
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.useVolga = false
        wizard.useMailru = true
        wizard.useCups = true
        wizard.mailruInput = "https://cloud.mail.ru/public/DEmN/ETbZW2MPY/?x=1"
        wizard.next()
        advanceUntilIdle()
        assertNull(wizard.error)
        assertEquals(WizardStep.Plan, wizard.step)
        val chosen = listOf(NodeTransport("mailru", "https://cloud.mail.ru/public/DEmN/ETbZW2MPY"), NodeTransport("cupsonline", "WyJyb29tLTEiXQ"))
        assertEquals(listOf(chosen), env.node.plannedTransports)
        assertEquals(TransportType.MAILRU, wizard.primaryType)

        // Turning the updater off asks the server again, and install says so.
        wizard.changeAutoUpdate(false)
        advanceUntilIdle()
        assertEquals(listOf(true, false), env.node.plannedAutoUpdate)
        wizard.install()
        advanceUntilIdle()
        assertEquals(WizardStep.Done, wizard.step, wizard.verifyFailed ?: wizard.error ?: "")
        assertEquals(
            listOf("of-test12", "mailru=https://cloud.mail.ru/public/DEmN/ETbZW2MPY cupsonline=WyJyb29tLTEiXQ", "31337", "autoUpdate=false", "p"),
            env.node.applied,
        )
        assertTrue(wizard.primaryUp)
        val saved = wizard.save()!!
        assertEquals(listOf(TransportType.MAILRU, TransportType.CUPSONLINE, TransportType.DIRECT), saved.carriers.map { it.type })
        assertEquals(1, env.node.roomsCreated)
    }

    @Test
    fun goingBackKeepsTheRooms() = runTest {
        val env = Env()
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.useCups = true
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        assertEquals(WizardStep.Plan, wizard.step)
        wizard.back()
        wizard.useVolga = false
        wizard.next()
        advanceUntilIdle()
        assertEquals(WizardStep.Plan, wizard.step)
        assertEquals(2, env.node.plannedTransports.size)
        assertEquals(1, env.node.roomsCreated)
        assertEquals(listOf(NodeTransport("cupsonline", "WyJyb29tLTEiXQ")), env.node.plannedTransports.last())
    }

    @Test
    fun directOnlyNeedsNothingElse() = runTest {
        val env = Env()
        env.connection.active = "direct"
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.useVolga = false
        wizard.next()
        advanceUntilIdle()
        assertEquals(listOf(emptyList()), env.node.plannedTransports)
        wizard.install()
        advanceUntilIdle()
        assertEquals(WizardStep.Done, wizard.step, wizard.verifyFailed ?: wizard.error ?: "")
        assertNull(wizard.primaryType)
        assertTrue(wizard.primaryUp)
    }

    @Test
    fun badMailruLinkOrMissingYandexDocumentStayOnTransports() = runTest {
        val env = Env()
        val wizard = NodeWizardModel(env.container, this)
        env.settings.update { it.copy(knownHostKeys = mapOf("$serverIp:22" to "SHA256:new")) }
        wizard.host = serverIp
        wizard.password = "p"
        wizard.connect()
        advanceUntilIdle()
        wizard.next()
        advanceUntilIdle()
        assertEquals(WizardStep.Document, wizard.step)
        assertTrue(wizard.error!!.contains("документ Яндекса"), wizard.error)

        wizard.useMailru = true
        wizard.mailruInput = "https://cloud.mail.ru/home/doc.docx"
        wizard.documentInput = docUrl
        wizard.checkDocument()
        advanceUntilIdle()
        assertEquals(WizardStep.Document, wizard.step)
        assertTrue(wizard.error!!.startsWith("Нужна публичная ссылка Mail.ru"), wizard.error)
        // The Yandex document is kept: fixing the link is enough.
        assertEquals(docUrl, wizard.documentUrl)
        wizard.mailruInput = "https://cloud.mail.ru/public/DEmN/ETbZW2MPY"
        wizard.next()
        advanceUntilIdle()
        assertEquals(WizardStep.Plan, wizard.step)
        assertEquals(listOf("vyandex", "mailru"), env.node.plannedTransports.last().map { it.type })
    }

    @Test
    fun transportNames() {
        assertEquals("Direct", NodeTransports.describe(emptyList()))
        assertEquals("Volga, Mail.ru и Direct", NodeTransports.describe(listOf(TransportType.VYANDEX, TransportType.MAILRU)))
        assertEquals("https://cloud.mail.ru/public/a1/b2", NodeTransports.cleanMailru(" https://cloud.mail.ru/public/a1/b2/?x#y "))
        assertNull(NodeTransports.cleanMailru("https://cloud.mail.ru.evil/public/a1/b2"))
    }

    @Test
    fun servers() {
        val a = KnownServer("a", 22, "root")
        val b = KnownServer("b", 22, "root")
        assertEquals(listOf(b, a), NodeServers.remember(listOf(a), b))
        assertEquals(listOf(KnownServer("A", 22, "admin"), b), NodeServers.remember(listOf(a, b), KnownServer("A", 22, "admin")))
        assertEquals(NodeServers.MAX_KNOWN, NodeServers.remember((1..9).map { KnownServer("h$it", 22, "u") }, a).size)
        assertNull(NodeServers.port("0"))
        assertNull(NodeServers.port("70000"))
        assertEquals(2222, NodeServers.port(" 2222 "))
    }

    private class Env(exitIp: String = "203.0.113.10", sudoFails: Boolean = false) {
        val settings = FakeSettings()
        val profiles = FakeProfiles()
        val connection = FakeConnection(exitIp)
        val node = FakeNode(sudoFails)
        val platform = FakePlatform()
        val container = AppContainer(profiles, settings, connection, platform, FakeShareLinkCodec(), node)
    }

    private class FakeNode(private val sudoFails: Boolean) : NodeWizardService {
        val applied = mutableListOf<String>()
        var connects = 0
        /** The SSH connection is gone: calls fail until the next connect. */
        var dropped = false
        var planError: String? = null
        var closed = false
        private val codec = FakeShareLinkCodec()

        override suspend fun connect(target: SshTarget): ServerProbe {
            if (target.hostKey != "SHA256:new") throw NodeWizardException("новый сервер", hostKey = "SHA256:new", trust = true)
            connects++
            dropped = false
            return ServerProbe(arch = "amd64", os = "Debian 12", systemd = true, sudo = "password")
        }

        override suspend fun newChannel() = NewChannel("of-test12", "ab".repeat(32))

        val plannedTransports = mutableListOf<List<NodeTransport>>()
        val plannedAutoUpdate = mutableListOf<Boolean>()
        var roomsCreated = 0

        override suspend fun plan(channel: String, transports: List<NodeTransport>, autoUpdate: Boolean): NodePlan {
            if (dropped) throw NodeWizardException("скрипт установки не ответил: ")
            planError?.let { throw NodeWizardException(it) }
            plannedTransports += transports
            plannedAutoUpdate += autoUpdate
            return NodePlan(channel = channel, port = 31337, actions = listOf("Установить ядро"))
        }

        override suspend fun apply(
            channel: NewChannel,
            transports: List<NodeTransport>,
            port: Int,
            autoUpdate: Boolean,
            sudoPassword: String,
        ) {
            if (dropped) throw NodeWizardException("не удалось передать конфигурацию на сервер")
            if (sudoFails) throw NodeWizardException("sudo не принял пароль", sudo = true)
            applied += listOf(channel.id, transports.joinToString(" ") { "${it.type}=${it.url}" }, port.toString(),
                "autoUpdate=$autoUpdate", sudoPassword)
        }

        override suspend fun createCupsRooms(): String {
            roomsCreated++
            return "WyJyb29tLTEiXQ"
        }

        override suspend fun remove(channel: String, sudoPassword: String) = Unit
        override suspend fun checkDocument(documentUrl: String) = Unit

        override suspend fun shareLink(name: String, key: String, host: String, port: Int, transports: List<NodeTransport>) =
            codec.encode(
                ShareConfig(
                    name = name, negotiate = true, secret = key,
                    context = transports.firstOrNull { it.type != "cupsonline" }?.url ?: "http://#",
                    transports = transports.mapIndexed { i, t -> ShareTransport(type = t.type, url = t.url, priority = 100 - 10 * i) } +
                        ShareTransport(type = "direct", dial = "$host:$port", priority = 50),
                ),
            )

        override suspend fun resolve(host: String) = setOf(host)
        override fun close() { closed = true }

        val notes = mutableListOf<String>()
        private val _logs = MutableStateFlow<List<LogLine>>(emptyList())
        override val logs: StateFlow<List<LogLine>> = _logs
        override fun clearLogs() { _logs.value = emptyList() }
        override fun note(text: String, level: LogLevel) { notes += text }
    }

    private class FakeConnection(private val exitIp: String) : ConnectionService {
        /** The carrier the session runs on once connected. */
        var active = "vyandex"
        /** How many exit address checks fail (502 from the core) before one works. */
        var failingChecks = 0
        var checks = 0
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
            traffic.value = TrafficStats(activeTransport = active, live = true)
        }

        override fun disconnect() {
            state.value = ConnectionState.Idle
            exitAddress.value = ExitAddress.Unknown
        }

        override fun refreshExitAddress() { exitAddress.value = check() }

        private fun check(): ExitAddress =
            if (checks++ < failingChecks) ExitAddress.Unavailable("Tunnel failed, got: 502") else ExitAddress.Known(exitIp)
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
        /** Moves [now] forward, as if the user had waited. */
        var skipped = 0L
        val opened = mutableListOf<String>()
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
        override fun openUrl(url: String) { opened += url }
        override fun newSecret() = "00".repeat(32)
        override fun now() = System.currentTimeMillis() + skipped
        override suspend fun latestRelease(): String? = null
    }
}
