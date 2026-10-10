package io.openflux.desktop

import io.openflux.desktop.platform.WindowsCoreElevation
import java.io.File
import java.nio.file.Files
import java.util.Base64
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class WindowsCoreElevationTest {
    private val windows = System.getProperty("os.name").lowercase().contains("win")

    @Test
    fun argumentsQuoteAsCommandLineToArgvReadsThem() {
        assertEquals("--role=client", WindowsCoreElevation.argv("--role=client"))
        assertEquals("\"\"", WindowsCoreElevation.argv(""))
        assertEquals("\"--config=C:\\Users\\Ivan Petrov\\p.conf\"", WindowsCoreElevation.argv("--config=C:\\Users\\Ivan Petrov\\p.conf"))
        // Backslashes before a quote double; trailing ones before the closing quote too.
        assertEquals("\"a\\\\\\\"b\"", WindowsCoreElevation.argv("a\\\"b"))
        assertEquals("\"C:\\dir with space\\\\\"", WindowsCoreElevation.argv("C:\\dir with space\\"))
        // No space or quote: backslashes stay as they are.
        assertEquals("C:\\a\\b\\", WindowsCoreElevation.argv("C:\\a\\b\\"))
    }

    @Test
    fun powershellLiteralsDoubleEveryKindOfSingleQuote() {
        assertEquals("'C:\\O''Brien\\x'", WindowsCoreElevation.ps("C:\\O'Brien\\x"))
        assertEquals("'a\u2019\u2019b'", WindowsCoreElevation.ps("a\u2019b"))
    }

    @Test
    fun launcherElevatesAnEncodedWatcher() {
        val cmd = WindowsCoreElevation.command(
            listOf("C:\\Program Files\\OpenFlux\\openflux.exe", "--url=https://x/?a=1&b='2'"),
            File("C:\\rt"), File("C:\\rt\\o.log"), File("C:\\rt\\e.log"), File("C:\\rt\\stop"), 4242,
        )
        assertEquals("powershell.exe", cmd.first())
        assertEquals("-EncodedCommand", cmd[cmd.size - 2])
        val launcher = String(Base64.getDecoder().decode(cmd.last()), Charsets.UTF_16LE)
        assertTrue("-Verb RunAs" in launcher && WindowsCoreElevation.DECLINED in launcher, launcher)
        val inner = Regex("-EncodedCommand ([A-Za-z0-9+/=]+)'").find(launcher)!!.groupValues[1]
        val watcher = String(Base64.getDecoder().decode(inner), Charsets.UTF_16LE)
        assertTrue("FileName = 'C:\\Program Files\\OpenFlux\\openflux.exe'" in watcher, watcher)
        assertTrue("Arguments = '--url=https://x/?a=1&b=''2'''" in watcher, watcher)
        assertTrue("Get-Process -Id 4242" in watcher && "Test-Path -LiteralPath ${WindowsCoreElevation.ps(File("C:\\rt\\stop").absolutePath)}" in watcher, watcher)
        assertTrue(WindowsCoreElevation.cancelled(WindowsCoreElevation.DECLINED))
    }

    // The elevated side, run here without UAC: only on Windows (the release workflow's Windows job).

    private val dir: File = Files.createTempDirectory("of win'elev").toFile()

    private fun watch(core: List<String>, appPid: Long): Triple<Process, File, File> {
        val out = File(dir, "o.log").apply { writeText("") }
        val err = File(dir, "e.log").apply { writeText("") }
        val stop = File(dir, "stop")
        val script = WindowsCoreElevation.watchScript(core, dir, out, err, stop, appPid)
        val process = ProcessBuilder("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", WindowsCoreElevation.encoded(script))
            .redirectErrorStream(true).start()
        return Triple(process, out, stop)
    }

    private fun waitFor(what: String, check: () -> Boolean) {
        val deadline = System.currentTimeMillis() + 20_000
        while (!check()) {
            check(System.currentTimeMillis() < deadline) {
                "timed out waiting for $what; out: ${File(dir, "o.log").readText()} err: ${File(dir, "e.log").readText()}"
            }
            Thread.sleep(100)
        }
    }

    /**
     * A stand-in core: prints its arguments one per line, then waits. A
     * one-file Java program (java runs the source), so its command line
     * stays short and Java parses it the way the core's Go runtime does.
     */
    private fun fakeCore(vararg args: String): List<String> {
        val source = File(dir, "FakeCore.java")
        source.writeText(
            """
            public class FakeCore {
                public static void main(String[] args) throws Exception {
                    for (String a : args) System.out.println("arg:" + a);
                    System.out.flush();
                    Thread.sleep(60000);
                }
            }
            """.trimIndent(),
        )
        return listOf(ProcessHandle.current().info().command().get(), source.absolutePath) + args
    }

    /** Everything the watcher printed, for failure messages. */
    private fun Process.said(): String = runCatching { inputStream.bufferedReader().readText() }.getOrDefault("")

    @Test
    fun stopFileEndsTheCoreAndArgumentsArriveIntact() {
        if (!windows) return
        val (process, out, stop) = watch(fakeCore("C:\\Users\\Ivan Petrov\\p.conf", "a\"b", "x&y"), ProcessHandle.current().pid())
        waitFor("start") { out.readText().contains("arg:x&y") }
        assertEquals(listOf("arg:C:\\Users\\Ivan Petrov\\p.conf", "arg:a\"b", "arg:x&y"), out.readLines().filter { it.isNotBlank() })
        Thread.sleep(1000)
        assertTrue(process.isAlive)
        stop.createNewFile()
        assertTrue(process.waitFor(20, TimeUnit.SECONDS))
    }

    @Test
    fun theAppGoingAwayEndsTheCore() {
        if (!windows) return
        val app = ProcessBuilder("powershell.exe", "-NoProfile", "-Command", "Start-Sleep -Seconds 60").start()
        val (process, out, _) = watch(fakeCore("x"), app.pid())
        waitFor("start") { out.readText().contains("arg:x") }
        app.destroyForcibly().waitFor()
        assertTrue(process.waitFor(20, TimeUnit.SECONDS))
    }

    @Test
    fun theCoresExitCodeComesBack() {
        if (!windows) return
        val (process, _, _) = watch(listOf("cmd.exe", "/c", "exit 3"), ProcessHandle.current().pid())
        assertTrue(process.waitFor(20, TimeUnit.SECONDS))
        assertEquals(3, process.exitValue(), process.said())
    }
}

