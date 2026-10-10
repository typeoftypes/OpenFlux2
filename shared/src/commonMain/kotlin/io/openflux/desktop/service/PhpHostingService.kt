package io.openflux.desktop.service

import io.openflux.desktop.model.FtpTarget
import io.openflux.desktop.model.LogLevel
import io.openflux.desktop.model.LogLine
import io.openflux.desktop.model.PhpHostingException
import io.openflux.desktop.model.PhpInstalled
import io.openflux.desktop.model.PhpNodeState
import io.openflux.desktop.model.PhpProbe
import io.openflux.desktop.model.PhpProgress
import io.openflux.desktop.model.PhpRoom
import io.openflux.desktop.model.PhpStatus
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.flow.updateAndGet
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonObjectBuilder
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

/**
 * How a step reaches the core: the desktop runs the core's `--node-wizard`
 * helper (methods `php.*`), Android calls the bridge's PhpCall. Either way the
 * answer is the core's JSON, `{"ok":true,"data":...}` or `{"ok":false,"error":
 * ...,"code":...,"param":...}`, and upload progress arrives through [onProgress].
 */
interface PhpCallTransport {
    suspend fun call(method: String, params: JsonObject, onProgress: (PhpProgress) -> Unit): JsonObject
    fun close()
}

/** A transport for a build that has no core to ask (and for tests that never reach it). */
object NoPhpTransport : PhpCallTransport {
    override suspend fun call(method: String, params: JsonObject, onProgress: (PhpProgress) -> Unit): JsonObject =
        throw PhpHostingException("bad_params", method, "no core")

    override fun close() = Unit
}

/**
 * The hosting wizard's server side: every step is one call to the core (its
 * phphost package decides, this only carries the answer), and a failure is a
 * [PhpHostingException] whose code the app words. The password and the node's
 * token never go into the log.
 */
open class PhpHostingService(
    private val transport: PhpCallTransport = NoPhpTransport,
    /** Wall-clock milliseconds, for the log's timeline (the platform supplies it). */
    private val clock: () -> Long = { 0L },
) {
    private val json = Json { ignoreUnknownKeys = true }
    private val lineIds = MutableStateFlow(0L)
    private val _logs = MutableStateFlow<List<LogLine>>(emptyList())

    /** The trace of this attempt, for the Logs tab. */
    open val logs: StateFlow<List<LogLine>> = _logs.asStateFlow()

    open fun clearLogs() {
        _logs.value = emptyList()
    }

    open fun note(text: String, level: LogLevel = LogLevel.Info) = log(level, text)

    open suspend fun probe(ftp: FtpTarget): PhpProbe =
        data("probe", "${ftp.user}@${ftp.host}:${ftp.port}", PhpProbe.serializer(), { ftp(ftp) })

    open suspend fun deploy(ftp: FtpTarget, token: String = "", onProgress: (PhpProgress) -> Unit = {}): PhpInstalled =
        data("deploy", "${ftp.user}@${ftp.host}:${ftp.port}", PhpInstalled.serializer(), {
            ftp(ftp)
            if (token.isNotEmpty()) put("token", token)
        }, onProgress)

    open suspend fun remove(ftp: FtpTarget) {
        call("remove", "${ftp.user}@${ftp.host}", { ftp(ftp) })
    }

    open suspend fun check(site: String, token: String, carrier: String): PhpStatus =
        data("check", site, PhpStatus.serializer(), { site(site, token, carrier) })

    /** [quiet]: a failure is logged as debug, not as an error (the automatic start before a connection, often out of reach). */
    open suspend fun start(site: String, token: String, carrier: String, target: String, chain: Boolean = true, quiet: Boolean = false): PhpNodeState =
        data("start", site, PhpNodeState.serializer(), {
            site(site, token, carrier)
            put("target", target)
            put("chain", chain)
        }, quiet = quiet)

    open suspend fun stop(site: String, token: String, carrier: String, target: String) {
        call("stop", site, { site(site, token, carrier); put("target", target) })
    }

    open suspend fun node(site: String, token: String, carrier: String, target: String): PhpNodeState =
        data("node", site, PhpNodeState.serializer(), { site(site, token, carrier); put("target", target) })

    /** The node's control panel for a browser (the core makes the address; opening it does not start the node). */
    open suspend fun page(site: String, token: String, carrier: String, target: String): String {
        val reply = call("page", site, { site(site, token, carrier); put("target", target) }, quiet = true)
        return reply.getValue("data").jsonObject["url"]?.jsonPrimitive?.content
            ?: throw PhpHostingException("bad_params", "page", "no address in the answer")
    }

    /** A new cups.online room for the node and its clients. */
    open suspend fun newRoom(): PhpRoom = data("newRoom", "", PhpRoom.serializer(), {})

    /** The `openflux://` link a device scans to use this node (stream mode, the core makes it). */
    open suspend fun link(name: String, carrier: String, target: String): String {
        val reply = call("link", carrier, { put("name", name); put("carrier", carrier); put("target", target) })
        return reply.getValue("data").jsonObject["link"]?.jsonPrimitive?.content
            ?: throw PhpHostingException("bad_params", "link", "no link in the answer")
    }

    open fun close() = transport.close()

    // ---- plumbing ----

    private fun JsonObjectBuilder.ftp(ftp: FtpTarget) {
        put("ftp", buildJsonObject {
            put("host", ftp.host.trim())
            put("port", ftp.port)
            put("user", ftp.user.trim())
            put("password", ftp.password)
            put("tls", ftp.tls)
            put("dir", ftp.dir)
        })
    }

    private fun JsonObjectBuilder.site(site: String, token: String, carrier: String) {
        put("url", site)
        put("token", token)
        put("carrier", carrier)
    }

    private suspend fun <T> data(
        method: String,
        summary: String,
        serializer: KSerializer<T>,
        params: JsonObjectBuilder.() -> Unit,
        onProgress: (PhpProgress) -> Unit = {},
        quiet: Boolean = false,
    ): T {
        val reply = call(method, summary, params, onProgress, quiet)
        val data: JsonElement = reply["data"] ?: throw PhpHostingException("bad_params", method, "no data in the answer")
        return json.decodeFromJsonElement(serializer, data)
    }

    private suspend fun call(
        method: String,
        summary: String = "",
        params: JsonObjectBuilder.() -> Unit,
        onProgress: (PhpProgress) -> Unit = {},
        quiet: Boolean = false,
    ): JsonObject {
        val label = if (summary.isEmpty()) method else "$method $summary"
        log(LogLevel.Debug, "хостинг: → $label")
        val reply = try {
            transport.call(method, buildJsonObject(params), onProgress)
        } catch (e: PhpHostingException) {
            log(if (quiet) LogLevel.Debug else LogLevel.Error, "хостинг: ← $label ${e.code}")
            throw e
        }
        if (reply["ok"]?.jsonPrimitive?.booleanOrNull != true) {
            val code = reply["code"]?.jsonPrimitive?.content.orEmpty()
            val param = reply["param"]?.jsonPrimitive?.content.orEmpty()
            val detail = reply["error"]?.jsonPrimitive?.content.orEmpty()
            log(if (quiet) LogLevel.Debug else LogLevel.Error, "хостинг: ← $label ошибка ${code.ifEmpty { "?" }}${if (param.isNotEmpty() && code != "ftp_login") " ($param)" else ""}: $detail")
            throw PhpHostingException(code.ifEmpty { "unknown" }, param, detail)
        }
        log(LogLevel.Debug, "хостинг: ← $label ok")
        return reply
    }

    private fun log(level: LogLevel, text: String) {
        // Negative ids: never collide with ConnectionService's own (positive) ids when merged for the Logs tab.
        val line = LogLine(-1_000_000 - lineIds.updateAndGet { it + 1 }, clock(), text, level)
        _logs.update { (it + line).takeLast(MAX_LOG_LINES) }
    }

    companion object {
        private const val MAX_LOG_LINES = 2000
    }
}
