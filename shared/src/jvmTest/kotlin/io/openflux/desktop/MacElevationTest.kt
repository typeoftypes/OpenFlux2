package io.openflux.desktop

import io.openflux.desktop.platform.MacElevation
import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** The root shell around the core, run here without osascript (and without root). */
class MacElevationTest {
    private val windows = System.getProperty("os.name").lowercase().contains("win")
    private val dir: File = Files.createTempDirectory("of mac'elev").toFile()

    /** A stand-in core: prints its arguments, cleans up on SIGTERM like the real one. */
    private val fakeCore = listOf(
        "/bin/sh", "-c",
        "echo \"started \$1\"; trap 'echo \"routes restored\"; exit 0' TERM; while :; do sleep 0.1; done",
        "core", "it's \"quoted\" \$HOME",
    )

    private fun start(appPid: Long, core: List<String> = fakeCore): Triple<Process, File, File> {
        val log = File(dir, "core.log").apply { writeText("") }
        val stop = File(dir, "stop")
        val script = MacElevation.watchScript(core, log, stop, appPid)
        return Triple(ProcessBuilder("/bin/sh", "-c", script).redirectErrorStream(true).start(), log, stop)
    }

    private fun waitFor(what: String, check: () -> Boolean) {
        val deadline = System.currentTimeMillis() + 5000
        while (!check()) {
            check(System.currentTimeMillis() < deadline) { "timed out waiting for $what" }
            Thread.sleep(50)
        }
    }

    @Test
    fun stopFileStopsTheCoreWhichCleansUp() {
        if (windows) return
        val (process, log, stop) = start(ProcessHandle.current().pid())
        waitFor("start") { log.readText().contains("started") }
        assertEquals("started it's \"quoted\" \$HOME\n", log.readText(), "arguments reach the core unchanged")
        Thread.sleep(500)
        assertTrue(process.isAlive, "runs until told to stop")
        stop.createNewFile()
        assertTrue(process.waitFor(5, TimeUnit.SECONDS))
        assertEquals(0, process.exitValue())
        assertTrue(log.readText().endsWith("routes restored\n"), log.readText())
    }

    @Test
    fun theAppGoingAwayStopsTheCore() {
        if (windows) return
        val app = ProcessBuilder("/bin/sh", "-c", "sleep 30").start()
        val (process, log, _) = start(app.pid())
        waitFor("start") { log.readText().contains("started") }
        app.destroyForcibly().waitFor()
        assertTrue(process.waitFor(5, TimeUnit.SECONDS))
        assertTrue(log.readText().contains("routes restored"))
    }

    @Test
    fun theCoresExitCodeComesBack() {
        if (windows) return
        val (process, log, _) = start(ProcessHandle.current().pid(), listOf("/bin/sh", "-c", "echo fatal: no utun; exit 3"))
        assertTrue(process.waitFor(5, TimeUnit.SECONDS))
        assertEquals(3, process.exitValue())
        assertEquals("fatal: no utun\n", log.readText())
    }

    @Test
    fun osascriptCommandQuotesForAppleScript() {
        val cmd = MacElevation.command(listOf("/Apps/Open \"Flux\"/core", "--x=a\\b"), File("/l"), File("/s"), 42)
        assertEquals("/usr/bin/osascript", cmd[0])
        val apple = cmd[2]
        assertTrue(apple.startsWith("do shell script \"") && apple.endsWith("with administrator privileges"))
        assertTrue("'/Apps/Open \\\"Flux\\\"/core' '--x=a\\\\b'" in apple, apple)
        assertTrue(MacElevation.cancelled("0:120: execution error: User canceled. (-128)"))
        assertFalse(MacElevation.cancelled("utun up"))
    }
}
