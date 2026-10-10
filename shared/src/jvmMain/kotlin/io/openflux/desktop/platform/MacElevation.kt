package io.openflux.desktop.platform

import java.io.File

/**
 * Root for the core on macOS, which its utun client (the full tunnel) needs
 * for the interface and the routes. OpenFlux itself stays a normal app: each
 * full-tunnel connection starts the core through `osascript … with
 * administrator privileges`, and macOS asks for an administrator's password.
 *
 * The app cannot signal a root process, so a small root shell watches over
 * the core: it stops the core (SIGTERM, which puts the routes back) when the
 * app creates the stop file or when the app is gone.
 */
object MacElevation {
    val mac: Boolean = System.getProperty("os.name").lowercase().contains("mac")

    /** OpenFlux already runs as root (started with sudo): no password needed. */
    val root: Boolean by lazy { System.getProperty("user.name") == "root" }

    /**
     * The command that runs [core] as root. Its output goes to [log] (the
     * app reads it from there), [stop] appearing stops it, and so does the
     * process [appPid] exiting. The command ends when the core does, with
     * the core's exit code.
     */
    fun command(core: List<String>, log: File, stop: File, appPid: Long): List<String> {
        val script = watchScript(core, log, stop, appPid)
        val apple = "do shell script \"" + appleString(script) + "\" with prompt \"" +
            appleString(PROMPT) + "\" with administrator privileges"
        return listOf("/usr/bin/osascript", "-e", apple)
    }

    /** The root shell: starts the core in the background and stops it when told to. */
    internal fun watchScript(core: List<String>, log: File, stop: File, appPid: Long): String = buildString {
        append("exec >>").append(shell(log.absolutePath)).append(" 2>&1 </dev/null; ")
        append(core.joinToString(" ", transform = ::shell)).append(" & c=\$!; s=0; ")
        append("while kill -0 \$c 2>/dev/null; do ")
        append("if [ \$s = 0 ] && { [ -e ").append(shell(stop.absolutePath)).append(" ] || ! kill -0 ").append(appPid)
        append(" 2>/dev/null; }; then kill -TERM \$c; s=1; fi; sleep 0.3; done; ")
        append("wait \$c")
    }

    /** Whether osascript's output says the password dialog was cancelled. */
    fun cancelled(line: String): Boolean = line.contains("(-128)") || line.contains("User canceled", ignoreCase = true)

    /** One shell word: single quotes, a quote inside as '\''. */
    internal fun shell(s: String): String = "'" + s.replace("'", "'\\''") + "'"

    /** The inside of an AppleScript string literal. */
    internal fun appleString(s: String): String = s.replace("\\", "\\\\").replace("\"", "\\\"")

    private const val PROMPT = "OpenFlux направляет весь трафик компьютера через ноду: для этого ядру нужны права администратора."
}
