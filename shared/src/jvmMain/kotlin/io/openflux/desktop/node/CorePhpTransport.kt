package io.openflux.desktop.node

import io.openflux.desktop.core.CoreBinary
import io.openflux.desktop.model.PhpHostingException
import io.openflux.desktop.model.PhpProgress
import io.openflux.desktop.service.PhpCallTransport
import io.openflux.desktop.service.SettingsRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put
import java.io.BufferedReader
import java.io.BufferedWriter

/**
 * The hosting wizard's steps through the core's `--node-wizard` helper (the
 * same JSON-lines protocol as the VDS wizard, methods `php.*`): one helper
 * process for as long as the wizard or the connection needs it, secrets only
 * through the pipe. While a step runs, the core sends extra lines
 * `{"id":N,"progress":{...}}` before its answer; they drive the upload bar.
 */
class CorePhpTransport(
    private val settings: SettingsRepository,
    private val binary: CoreBinary,
) : PhpCallTransport {
    private val json = Json { ignoreUnknownKeys = true }
    private val lock = Mutex()
    private var helper: Helper? = null
    private var nextId = 0L

    private class Helper(val process: Process, val input: BufferedWriter, val output: BufferedReader)

    override suspend fun call(method: String, params: JsonObject, onProgress: (PhpProgress) -> Unit): JsonObject =
        withContext(Dispatchers.IO) {
            lock.withLock {
                val h = helper?.takeIf { it.process.isAlive } ?: start().also { helper = it }
                val id = ++nextId
                val request = buildJsonObject {
                    put("id", id)
                    put("method", "php.$method")
                    put("params", params)
                }
                try {
                    h.input.write(request.toString())
                    h.input.newLine()
                    h.input.flush()
                    while (true) {
                        val line = h.output.readLine() ?: break
                        val reply = json.parseToJsonElement(line).jsonObject
                        if (reply["id"]?.toString()?.toLongOrNull() != id) continue
                        reply["progress"]?.let { p ->
                            onProgress(json.decodeFromJsonElement(PhpProgress.serializer(), p))
                            continue
                        }
                        return@withLock reply
                    }
                } catch (e: java.io.IOException) {
                    // falls through: the helper is gone
                }
                helper = null
                throw PhpHostingException("bad_params", method, "the core did not answer")
            }
        }

    override fun close() {
        val current = helper
        helper = null
        if (current != null) {
            runCatching { current.input.close() }
            if (!current.process.waitFor(5, java.util.concurrent.TimeUnit.SECONDS)) current.process.destroyForcibly()
        }
    }

    private fun start(): Helper {
        val core = binary.resolve(settings.settings.value)
            ?: throw PhpHostingException("bad_params", "core", "no core binary")
        val process = ProcessBuilder(core.absolutePath, "--node-wizard")
            .redirectError(ProcessBuilder.Redirect.DISCARD)
            .start()
        return Helper(
            process,
            process.outputStream.bufferedWriter(Charsets.UTF_8),
            process.inputStream.bufferedReader(Charsets.UTF_8),
        )
    }
}
