package io.openflux.desktop.node

import io.openflux.desktop.core.CoreBinary
import io.openflux.desktop.model.LogLevel
import io.openflux.desktop.model.LogLine
import io.openflux.desktop.model.NewChannel
import io.openflux.desktop.model.NodePlan
import io.openflux.desktop.model.NodeTransport
import io.openflux.desktop.model.NodeWizardException
import io.openflux.desktop.model.ServerProbe
import io.openflux.desktop.model.SshTarget
import io.openflux.desktop.ui.BrowserPage
import io.openflux.desktop.service.NodeWizardService
import io.openflux.desktop.service.SettingsRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import java.io.BufferedReader
import java.io.BufferedWriter
import java.net.InetAddress
import java.util.concurrent.atomic.AtomicLong

/**
 * The wizard's server side through the core's --node-wizard: one helper
 * process for the wizard's lifetime, JSON lines on stdin/stdout. Secrets go
 * only through the pipe, never on the command line or into the log.
 */
class CoreNodeWizard(
    private val settings: SettingsRepository,
    private val binary: CoreBinary,
) : NodeWizardService {
    private val json = Json { ignoreUnknownKeys = true }
    private val lock = Mutex()
    private var helper: Helper? = null
    private var nextId = 0L

    private val lineIds = AtomicLong()
    private val _logs = MutableStateFlow<List<LogLine>>(emptyList())
    override val logs: StateFlow<List<LogLine>> = _logs.asStateFlow()

    private class Helper(val process: Process, val input: BufferedWriter, val output: BufferedReader)

    override suspend fun connect(target: SshTarget): ServerProbe {
        val reply = call("connect", "${target.host}:${target.port} (${target.user})") {
            put("host", target.host)
            put("port", target.port)
            put("user", target.user)
            put("password", target.password)
            put("privateKey", target.privateKey)
            put("passphrase", target.passphrase)
            put("hostKey", target.hostKey)
        }
        return json.decodeFromJsonElement(ServerProbe.serializer(), reply.getValue("probe"))
    }

    override suspend fun newChannel(): NewChannel {
        val reply = call("newChannel") {}
        return NewChannel(reply.string("channel"), reply.string("key"))
    }

    override suspend fun plan(channel: String, transports: List<NodeTransport>, autoUpdate: Boolean): NodePlan {
        val reply = call("plan", "channel=$channel ${transports.names()} autoUpdate=$autoUpdate") {
            put("channel", channel)
            put("channelPort", 0)
            put("transports", transports.json())
            put("autoUpdate", autoUpdate)
        }
        return json.decodeFromJsonElement(NodePlan.serializer(), reply.getValue("plan"))
    }

    override suspend fun apply(
        channel: NewChannel,
        transports: List<NodeTransport>,
        port: Int,
        autoUpdate: Boolean,
        sudoPassword: String,
    ) {
        call("apply", "channel=${channel.id} ${transports.names()} port=$port autoUpdate=$autoUpdate") {
            put("channel", channel.id)
            put("key", channel.key)
            put("transports", transports.json())
            put("channelPort", port)
            put("autoUpdate", autoUpdate)
            put("sudoPassword", sudoPassword)
        }
    }

    override suspend fun remove(channel: String, sudoPassword: String) {
        call("remove", "channel=$channel") {
            put("channel", channel)
            put("sudoPassword", sudoPassword)
        }
    }

    override suspend fun checkDocument(documentUrl: String) {
        call("checkDocument") { put("documentUrl", documentUrl) }
    }

    override suspend fun createCupsRooms(): String = call("createRooms") {}.string("rooms")

    override suspend fun shareLink(name: String, key: String, host: String, port: Int, transports: List<NodeTransport>): String =
        call("shareLink", "$host:$port ${transports.names()}") {
            put("name", name)
            put("key", key)
            put("host", host)
            put("channelPort", port)
            put("transports", transports.json())
        }.string("link")

    private fun List<NodeTransport>.json() = json.encodeToJsonElement(ListSerializer(NodeTransport.serializer()), this)

    /** For the log: the types only, links and rooms stay out. */
    private fun List<NodeTransport>.names() = "transports=" + joinToString(",") { it.type }.ifEmpty { "direct" }

    override suspend fun resolve(host: String): Set<String> = withContext(Dispatchers.IO) {
        runCatching { InetAddress.getAllByName(host).mapNotNull { it.hostAddress }.toSet() }
            .onSuccess { log(LogLevel.Debug, "мастер: resolve $host -> ${it.joinToString()}") }
            .onFailure { log(LogLevel.Warning, "мастер: resolve $host не удался: ${it.message}") }
            .getOrDefault(emptySet())
    }

    override fun close() {
        val current = helper
        helper = null
        if (current != null) {
            log(LogLevel.Info, "мастер: закрываю ядро")
            // Closing stdin ends the helper, which closes SSH and removes the
            // downloaded installer from the server.
            runCatching { current.input.close() }
            if (!current.process.waitFor(5, java.util.concurrent.TimeUnit.SECONDS)) current.process.destroyForcibly()
        }
    }

    override fun clearLogs() {
        _logs.value = emptyList()
    }

    override fun note(text: String, level: LogLevel) = log(level, text)

    private fun log(level: LogLevel, text: String) {
        // Negative ids: never collide with ConnectionService's own (positive) ids when merged for the Logs tab.
        val line = LogLine(-lineIds.incrementAndGet(), System.currentTimeMillis(), text, level)
        _logs.update { (it + line).takeLast(MAX_LOG_LINES) }
    }

    private suspend fun call(method: String, summary: String = "", params: kotlinx.serialization.json.JsonObjectBuilder.() -> Unit): JsonObject =
        withContext(Dispatchers.IO) {
            lock.withLock {
                val label = if (summary.isEmpty()) method else "$method $summary"
                log(LogLevel.Debug, "мастер: → $label")
                val h = helper?.takeIf { it.process.isAlive } ?: start().also { helper = it }
                val id = ++nextId
                val request = buildJsonObject {
                    put("id", id)
                    put("method", method)
                    put("params", buildJsonObject(params))
                }
                val line = try {
                    h.input.write(request.toString())
                    h.input.newLine()
                    h.input.flush()
                    h.output.readLine()
                } catch (e: java.io.IOException) {
                    null
                } ?: run {
                    helper = null
                    log(LogLevel.Error, "мастер: ← $label ядро не ответило")
                    throw NodeWizardException("Ядро OpenFlux не ответило мастеру")
                }
                val reply = json.parseToJsonElement(line).jsonObject
                if (reply["ok"]?.jsonPrimitive?.boolean != true) {
                    val error = reply["error"]?.jsonPrimitive?.content ?: "Ошибка мастера"
                    log(LogLevel.Error, "мастер: ← $label ошибка: $error")
                    throw NodeWizardException(
                        error,
                        hostKey = reply["hostKey"]?.jsonPrimitive?.content?.takeIf { it.isNotEmpty() },
                        trust = reply.flag("trust"),
                        mismatch = reply.flag("mismatch"),
                        sudo = reply.flag("sudo"),
                        captcha = reply.flag("captcha"),
                    )
                }
                log(LogLevel.Debug, "мастер: ← $label ok")
                reply
            }
        }

    private fun start(): Helper {
        val core = binary.resolve(settings.settings.value)
            ?: run {
                log(LogLevel.Error, "мастер: не найдено ядро OpenFlux")
                throw NodeWizardException("Не найдено ядро OpenFlux: укажите его в настройках")
            }
        log(LogLevel.Info, "мастер: запускаю ${core.absolutePath} --node-wizard")
        val process = ProcessBuilder(core.absolutePath, "--node-wizard")
            .redirectError(ProcessBuilder.Redirect.DISCARD)
            .start()
        return Helper(
            process,
            process.outputStream.bufferedWriter(Charsets.UTF_8),
            process.inputStream.bufferedReader(Charsets.UTF_8),
        )
    }

    private fun JsonObject.string(name: String) =
        this[name]?.jsonPrimitive?.content ?: throw NodeWizardException("Ядро не вернуло $name")

    private fun JsonObject.flag(name: String) = this[name]?.jsonPrimitive?.booleanOrNull == true

    companion object {
        private const val MAX_LOG_LINES = 2000
    }
}
