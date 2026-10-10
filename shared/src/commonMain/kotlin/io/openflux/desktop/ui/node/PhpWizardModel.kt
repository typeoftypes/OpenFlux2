package io.openflux.desktop.ui.node

import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import io.openflux.desktop.model.ConnectionState
import io.openflux.desktop.model.ExitAddress
import io.openflux.desktop.model.FtpTarget
import io.openflux.desktop.model.LogLevel
import io.openflux.desktop.model.NodeTransports
import io.openflux.desktop.model.PhpHostingException
import io.openflux.desktop.model.PhpHosts
import io.openflux.desktop.model.PhpInstalled
import io.openflux.desktop.model.PhpMessages
import io.openflux.desktop.model.PhpNodeRef
import io.openflux.desktop.model.PhpNodeState
import io.openflux.desktop.model.PhpProbe
import io.openflux.desktop.model.PhpProgress
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ProfileSource
import io.openflux.desktop.model.TransportType
import io.openflux.desktop.model.isActive
import io.openflux.desktop.model.profile
import io.openflux.desktop.service.AppContainer
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

enum class PhpStep(val number: Int) { Hosting(1), Channel(2), Install(3), Verify(4), Done(4) }

/**
 * "Без сервера": puts the exit on a web hosting (even a free one) over FTP and
 * hands back a verified profile. Steps: the hosting's FTP data and the site's
 * address; the channel (a cups.online room the wizard creates, or a Mail.ru
 * document); the install (upload, check the site, start the node); then a
 * real connection through the new node. A node already on the hosting (put
 * there by hand) is added with its site and access key instead of FTP. Every decision is the core's
 * (provision/phphost); this keeps the form and the order of steps, and words
 * what the core reports.
 *
 * The FTP password lives only in this object for the length of the wizard.
 */
@Stable
class PhpWizardModel(private val container: AppContainer, private val scope: CoroutineScope) {
    private val service = container.phpHosting
    private val settings = container.settings
    private val connection = container.connection

    var step by mutableStateOf(PhpStep.Hosting)
        private set
    /** What a running call is doing; null when idle. */
    var busy by mutableStateOf<String?>(null)
        private set
    var error by mutableStateOf<String?>(null)
    var notice by mutableStateOf<String?>(null)

    // Step 1: the hosting.
    /** The node is already on the hosting (uploaded by hand): its site and access key, no FTP. */
    var existing by mutableStateOf(false)
    var tokenInput by mutableStateOf("")
    var ftpHost by mutableStateOf("")
    var ftpPort by mutableStateOf("21")
    var ftpUser by mutableStateOf("")
    var ftpPassword by mutableStateOf("")
    var siteInput by mutableStateOf("")
    /** "auto" or "none": whether the FTP connection may be plain (some hostings have no TLS). */
    var ftpTls by mutableStateOf("auto")
    var folderInput by mutableStateOf("")
    /** A key the user chose for the new node (optional: empty keeps the installed node's key or makes one). */
    var chosenToken by mutableStateOf("")
    /** Shown when the core could not tell which folder is the site: the user picks. */
    var folderChoices by mutableStateOf<List<String>>(emptyList())
        private set
    var probe by mutableStateOf<PhpProbe?>(null)
        private set

    // Step 2: the channel.
    var name by mutableStateOf("")
    var carrier by mutableStateOf(TransportType.CUPSONLINE)
    var mailruInput by mutableStateOf("")
    /** What the node joins: the cups.online room's address, or the document link. */
    var target by mutableStateOf("")
        private set
    private var cupsRoomUrl = ""
    /** The cups.online room the node's address named (an existing node): the wizard keeps it instead of making one. */
    var knownRoom by mutableStateOf("")
        private set

    // Step 3: install.
    var progress by mutableStateOf<PhpProgress?>(null)
        private set
    var installed by mutableStateOf<PhpInstalled?>(null)
        private set
    /** The site as used from here on (a scheme and the host). */
    var siteUrl by mutableStateOf("")
        private set
    /** The node reports running. */
    var nodeRunning by mutableStateOf(false)
        private set
    /** What the node last said about itself: running, which generation serves, when it hands over. */
    var nodeState by mutableStateOf<PhpNodeState?>(null)
        private set

    // Step 4: verification and the result.
    var profile by mutableStateOf<Profile?>(null)
        private set
    var verifiedIp by mutableStateOf("")
        private set
    var verifyFailed by mutableStateOf<String?>(null)
        private set
    var shareLink by mutableStateOf("")
        private set
    var saved by mutableStateOf(false)
        private set

    private var job: Job? = null
    private var ftp: FtpTarget? = null

    /** Leaving now would leave a node on the hosting without a saved profile. */
    val unsaved: Boolean get() = installed != null && !saved

    /** The wizard put the files there itself, so it can take them away again (an existing node has no FTP here). */
    val canRemove: Boolean get() = !existing && installed != null

    /** The node's access key, shown by its ends only. */
    val maskedToken: String get() = PhpHosts.maskToken(installed?.token.orEmpty())

    /** How the FTP password travelled, when it was not protected well; null otherwise. */
    val securityNote: String? get() = PhpMessages.security(installed?.security ?: probe?.security.orEmpty())

    val preset get() = PhpHosts.presetFor(ftpHost)

    // ---- step 1: hosting ----

    /** Fills the form from a known hosting family. */
    fun usePreset(index: Int) {
        val p = PhpHosts.presets.getOrNull(index) ?: return
        ftpHost = p.ftpHost
    }

    fun probeHosting() {
        val problems = PhpHosts.ftpProblems(ftpHost, ftpPort, ftpUser, ftpPassword)
        val site = PhpHosts.siteUrl(siteInput)
        if (problems.isNotEmpty()) { error = problems.first(); return }
        if (site == null) { error = "Укажите адрес сайта на хостинге, например https://ваш-сайт.ru"; return }
        PhpHosts.chosenTokenProblem(chosenToken.trim())?.let { error = it; return }
        siteUrl = site
        val target = ftpTarget()
        launchCall("Проверяю вход на хостинг…", onFailure = { e ->
            if (e.candidates.isNotEmpty()) {
                folderChoices = e.candidates
                error = e.message
                true
            } else false
        }) {
            probe = service.probe(target)
            ftp = target
            folderChoices = emptyList()
            if (name.isBlank()) name = "Свой хостинг · ${target.host}"
            step = PhpStep.Channel
        }
    }

    /**
     * The address field of an existing node changed: a pasted page address (`…/mailruexit.php?k=…&url=…`)
     * fills in the key and the channel, so nothing has to be typed twice.
     */
    fun onExistingAddress(input: String) {
        siteInput = input.trim()
        val node = PhpHosts.nodeAddress(siteInput) ?: return
        if (node.token.isNotEmpty()) tokenInput = node.token
        when (node.carrier) {
            TransportType.CUPSONLINE -> {
                carrier = TransportType.CUPSONLINE
                if (node.target.isNotEmpty()) { knownRoom = node.target; cupsRoomUrl = node.target }
            }
            TransportType.MAILRU -> {
                carrier = TransportType.MAILRU
                if (node.target.isNotEmpty()) mailruInput = node.target
            }
            else -> Unit
        }
    }

    /** Step 1 for a node already on the hosting: the site and its key are enough. */
    fun useExisting() {
        val node = PhpHosts.nodeAddress(siteInput)
        if (node == null) { error = "Укажите адрес сайта с нодой или адрес её страницы, например https://ваш-сайт.ru"; return }
        PhpHosts.tokenProblem(tokenInput.trim())?.let { error = it; return }
        error = null
        siteUrl = node.site
        if (name.isBlank()) name = "Свой хостинг · ${node.site.substringAfter("://")}"
        step = PhpStep.Channel
    }

    /** The user picked the site's folder from the list; try again with it. */
    fun chooseFolder(dir: String) {
        folderInput = dir
        folderChoices = emptyList()
        error = null
        probeHosting()
    }

    private fun ftpTarget() = FtpTarget(
        host = PhpHosts.ftpHost(ftpHost) ?: ftpHost.trim(),
        port = ftpPort.trim().toIntOrNull() ?: 21,
        user = ftpUser.trim(),
        password = ftpPassword,
        tls = ftpTls,
        dir = folderInput.trim(),
    )

    // ---- step 2: channel ----

    fun back() {
        if (busy != null) return
        error = null
        notice = null
        if (step == PhpStep.Channel) step = PhpStep.Hosting
        if (step == PhpStep.Install && installed == null) step = PhpStep.Channel
    }

    fun prepareChannel() {
        error = null
        if (name.isBlank()) { error = "Назовите профиль"; return }
        when (carrier) {
            TransportType.CUPSONLINE -> {
                if (cupsRoomUrl.isNotEmpty()) { target = cupsRoomUrl; step = PhpStep.Install; return }
                launchCall("Создаю комнату cups.online…") {
                    cupsRoomUrl = service.newRoom().url
                    target = cupsRoomUrl
                    step = PhpStep.Install
                }
            }
            TransportType.MAILRU -> {
                val link = NodeTransports.cleanMailru(mailruInput)
                if (link == null) { error = "Нужна публичная ссылка на документ Mail.ru: https://cloud.mail.ru/public/…"; return }
                target = link
                step = PhpStep.Install
            }
            else -> error = "Этот транспорт не подходит для режима без сервера"
        }
    }

    // ---- step 3: install ----

    /** Uploads the node, checks that the site answers as it, starts it, then verifies the channel. */
    fun install() {
        if (existing) { attachExisting(); return }
        val ftp = ftp ?: return
        launchCall("Загружаю файлы на хостинг…") {
            progress = null
            val done = service.deploy(ftp, chosenToken.trim()) { p ->
                progress = p
                when (p.phase) {
                    "upload" -> if (p.of > 0) busy = "Загружаю файлы на хостинг: ${p.n} из ${p.of}…"
                    "retry" -> {
                        busy = "Хостинг оборвал передачу ${p.file}, отправляю заново…"
                        service.note("хостинг оборвал передачу, повторяю: ${p.note}", LogLevel.Warning)
                    }
                    "skipped" -> service.note("хостинг не принял необязательный файл, ставлю без него: ${p.note}", LogLevel.Warning)
                }
            }
            installed = done
            busy = "Проверяю, что сайт отвечает…"
            waitForSite(done.token)
            busy = "Запускаю ноду…"
            val state = service.start(siteUrl, done.token, carrier.cliName, target, chain = true)
            nodeState = state
            nodeRunning = state.running
            step = PhpStep.Verify
            verify()
        }
    }

    /** An existing node: check that the site answers as it with this key, start it, then verify the channel. */
    private fun attachExisting() {
        val token = tokenInput.trim()
        launchCall("Проверяю ноду на сайте…") {
            try {
                waitForSite(token)
            } catch (e: PhpHostingException) {
                throw if (e.code == "site_token") PhpHostingException("site_token_given") else e
            }
            installed = PhpInstalled(token = token, tokenReused = true)
            busy = "Запускаю ноду…"
            val state = service.start(siteUrl, token, carrier.cliName, target, chain = true)
            nodeState = state
            nodeRunning = state.running
            step = PhpStep.Verify
            verify()
        }
    }

    /**
     * A new domain can take minutes to start answering, and a fresh upload a
     * moment to be served: try for about a minute before giving up. Only
     * "the site does not answer" is worth waiting for; every other reason is final.
     */
    private suspend fun waitForSite(token: String) {
        var last: PhpHostingException? = null
        repeat(SITE_TRIES) { attempt ->
            try {
                service.check(siteUrl, token, carrier.cliName)
                return
            } catch (e: PhpHostingException) {
                if (e.code != "site_unreachable" && e.code != "site_not_phpbox") throw e
                last = e
                busy = "Жду, пока сайт заработает (${attempt + 1} из $SITE_TRIES)…"
                delay(SITE_RETRY_MS)
            }
        }
        throw last ?: PhpHostingException("site_unreachable", siteUrl)
    }

    // ---- step 4: verify ----

    /** Connects as the new profile and waits for a request to come out of the hosting. */
    fun retryVerify() {
        verifyFailed = null
        step = PhpStep.Verify
        launchCall("Подключаюсь через новую ноду…") { verify() }
    }

    private suspend fun verify() {
        val token = installed?.token.orEmpty()
        val candidate = Profile(
            id = container.profiles.newId(),
            name = name.trim(),
            transport = carrier,
            value = target,
            stream = true,
            source = ProfileSource.Node,
            createdAt = container.platform.now(),
            phpNode = PhpNodeRef(siteUrl, token),
        )
        profile = candidate
        shareLink = runCatching { service.link(candidate.name, carrier.cliName, target) }.getOrDefault("")
        verifiedIp = ""
        verifyFailed = null
        busy = "Подключаюсь через новую ноду…"
        try {
            connection.connect(candidate)
            val deadline = container.platform.now() + VERIFY_TIMEOUT_MS
            var started = false
            var lastProblem = ""
            while (verifiedIp.isEmpty()) {
                if (container.platform.now() > deadline) {
                    throw PhpHostingException(
                        "node_not_started",
                        detail = if (lastProblem.isNotEmpty()) "запрос через ноду не прошёл: $lastProblem" else "нода не ответила",
                    )
                }
                val state = connection.state.value
                if (state.profile?.id == candidate.id) started = true
                else if (started) throw IllegalStateException("Проверку прервало отключение или другое подключение")
                when {
                    !started -> Unit
                    state is ConnectionState.Failed -> throw IllegalStateException(state.message)
                    state is ConnectionState.Connected -> {
                        busy = if (lastProblem.isNotEmpty()) "Нода пока молчит, пробую ещё раз…" else "Открываю сайт через ноду…"
                        when (val address = connection.exitAddress.value) {
                            is ExitAddress.Known -> verifiedIp = address.ip
                            is ExitAddress.Unavailable -> {
                                lastProblem = address.reason
                                delay(RETRY_MS)
                                connection.refreshExitAddress()
                            }
                            else -> Unit
                        }
                    }
                }
                if (verifiedIp.isEmpty()) delay(POLL_MS)
            }
            step = PhpStep.Done
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            val state = connection.state.value
            if (state.profile?.id == candidate.id && state.isActive) connection.disconnect()
            verifyFailed = e.message ?: "Проверка не прошла"
        }
    }

    fun skipVerification() {
        verifyFailed = null
        step = PhpStep.Done
    }

    // ---- done ----

    /** Saves the profile here; returns it, or null when there is none. */
    fun save(): Profile? {
        val candidate = profile ?: return null
        val named = candidate.copy(name = name.trim().ifBlank { candidate.name })
        container.profiles.upsert(named)
        profile = named
        saved = true
        settings.update { it.copy(selectedProfileId = named.id) }
        return named
    }

    fun copyLink() = container.platform.setClipboardText(shareLink)

    /** The node's access key: needed to add this node on another device, or to open its page (…?k=key). */
    fun copyToken() = installed?.token?.let { container.platform.setClipboardText(it) }

    fun qr() = container.platform.qrMatrix(shareLink)

    /** Asks the node how it is: running, which generation serves now. */
    fun refreshNode() {
        val token = installed?.token ?: return
        launchCall("Спрашиваю ноду…") {
            val state = service.node(siteUrl, token, carrier.cliName, target)
            nodeState = state
            nodeRunning = state.running
        }
    }

    /** Opens the node's control panel (its page: state, generation, log, Start/Stop) in the browser. */
    fun openPanel() {
        val token = installed?.token ?: return
        launchCall("Открываю панель ноды…") {
            container.platform.openUrl(service.page(siteUrl, token, carrier.cliName, target))
        }
    }

    /** Stops the node on the hosting (the whole chain). */
    fun stopNode() {
        val token = installed?.token ?: return
        launchCall("Останавливаю ноду…") {
            service.stop(siteUrl, token, carrier.cliName, target)
            nodeRunning = false
            nodeState = null
            notice = "Нода остановлена. Подключение через неё больше не заработает, пока её не запустят снова."
        }
    }

    /** Deletes the node's files from the hosting (only those the wizard put there). */
    fun removeFromHosting() {
        val ftp = ftp ?: return
        val token = installed?.token
        launchCall("Удаляю ноду с хостинга…") {
            if (token != null) runCatching { service.stop(siteUrl, token, carrier.cliName, target) }
            service.remove(ftp)
            installed = null
            nodeRunning = false
            notice = "Файлы ноды удалены с хостинга."
        }
    }

    /** Ends the wizard; drops the test connection of an unsaved profile and forgets the password. */
    fun close() {
        job?.cancel()
        val state = connection.state.value
        if (!saved && profile != null && state.profile?.id == profile?.id && state.isActive) connection.disconnect()
        ftpPassword = ""
        tokenInput = ""
        ftp = null
        service.close()
    }

    /** Runs one step at a time. A [PhpHostingException] becomes [error] unless [onFailure] handled it. */
    private fun launchCall(message: String, onFailure: (PhpHostingException) -> Boolean = { false }, block: suspend () -> Unit) {
        if (busy != null) return
        busy = message
        error = null
        notice = null
        service.note(message)
        job = scope.launch {
            try {
                block()
                service.note("$message — готово", LogLevel.Success)
            } catch (e: CancellationException) {
                service.note("$message — отменено", LogLevel.Warning)
                throw e
            } catch (e: PhpHostingException) {
                service.note(e.message ?: "Ошибка установки", LogLevel.Error)
                if (!onFailure(e)) error = e.message
            } catch (e: Exception) {
                val text = e.message ?: "Ошибка установки"
                service.note(text, LogLevel.Error)
                error = text
            } finally {
                busy = null
            }
        }
    }

    companion object {
        private const val SITE_TRIES = 12
        private const val SITE_RETRY_MS = 5000L
        private const val VERIFY_TIMEOUT_MS = 150_000L
        private const val POLL_MS = 400L
        private const val RETRY_MS = 4000L
    }
}
