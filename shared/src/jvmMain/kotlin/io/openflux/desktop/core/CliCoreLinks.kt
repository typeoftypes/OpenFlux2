package io.openflux.desktop.core

import io.openflux.desktop.model.CoreLinks
import io.openflux.desktop.service.SettingsRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.TimeUnit

/**
 * The core's link reading and making on the desktop: `openflux --parse-link -`
 * and `--make-link -`, run with the core the settings choose. The link or
 * the configuration goes in on stdin (it carries the key: never on the
 * command line), share.Result JSON comes back on stdout.
 */
class CliCoreLinks(
    private val settings: SettingsRepository,
    private val binary: CoreBinary,
) : CoreLinks {
    override suspend fun read(link: String): String = run("--parse-link", link)

    override suspend fun make(configJson: String): String = run("--make-link", configJson)

    private suspend fun run(command: String, input: String): String = withContext(Dispatchers.IO) {
        val core = binary.resolve(settings.settings.value)
            ?: throw IllegalStateException("Не найдено ядро OpenFlux: укажите его в настройках")
        val process = ProcessBuilder(core.absolutePath, command, "-")
            .redirectError(ProcessBuilder.Redirect.DISCARD)
            .start()
        try {
            process.outputStream.bufferedWriter(Charsets.UTF_8).use { it.write(input) }
            // The answer is a few hundred bytes: it fits the pipe, so the
            // core exits before it is read.
            if (!process.waitFor(15, TimeUnit.SECONDS)) throw IllegalStateException("Ядро OpenFlux не ответило")
            process.inputStream.bufferedReader(Charsets.UTF_8).readText()
        } finally {
            process.destroy()
        }
    }
}
