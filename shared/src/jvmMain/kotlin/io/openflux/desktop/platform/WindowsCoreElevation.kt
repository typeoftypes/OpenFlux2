package io.openflux.desktop.platform

import java.io.File
import java.util.Base64

/**
 * Administrator rights for the core alone on Windows, which its Wintun
 * client (the full tunnel) needs, as [MacElevation] does on macOS: OpenFlux
 * stays a normal app, and each full-tunnel connection starts the core
 * through UAC.
 *
 * The app cannot stop an elevated process, so an elevated PowerShell
 * watches over the core: it ends the core when the app creates the stop
 * file or when the app is gone. Wintun removes the adapter, and with it
 * the routes, when the core's process ends. The core's output goes to two
 * files (stdout, stderr) the app follows.
 */
object WindowsCoreElevation {
    val windows: Boolean = System.getProperty("os.name").lowercase().contains("win")

    /** What the launcher prints when the UAC prompt was declined. */
    const val DECLINED = "OPENFLUX_UAC_DECLINED"

    /** The command that runs [core] elevated; it ends when the core does, with its exit code. */
    fun command(core: List<String>, dir: File, out: File, err: File, stop: File, appPid: Long): List<String> {
        val inner = encoded(watchScript(core, dir, out, err, stop, appPid))
        val launcher = """
            ${'$'}ErrorActionPreference = 'Stop'
            try {
                ${'$'}p = Start-Process -FilePath 'powershell.exe' -Verb RunAs -WindowStyle Hidden -PassThru -ArgumentList '$PS_FLAGS -EncodedCommand $inner'
            } catch {
                Write-Output '$DECLINED'
                exit 1223
            }
            ${'$'}null = ${'$'}p.Handle
            ${'$'}p.WaitForExit()
            exit ${'$'}p.ExitCode
        """.trimIndent()
        return listOf("powershell.exe") + PS_FLAGS.split(" ") + listOf("-EncodedCommand", encoded(launcher))
    }

    /**
     * The elevated side: starts the core hidden and stops it when told to.
     * System.Diagnostics.Process rather than Start-Process, which loses the
     * exit code of a process that ends at once when its output is
     * redirected; the output is copied to the files unbuffered, so the app
     * reads it as it comes.
     */
    internal fun watchScript(core: List<String>, dir: File, out: File, err: File, stop: File, appPid: Long): String {
        val args = core.drop(1).joinToString(" ", transform = ::argv)
        return """
            ${'$'}ErrorActionPreference = 'Stop'
            function Open-Log(${'$'}path) { New-Object System.IO.FileStream(${'$'}path, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write, [System.IO.FileShare]::ReadWrite, 1) }
            ${'$'}o = Open-Log ${ps(out.absolutePath)}
            ${'$'}e = Open-Log ${ps(err.absolutePath)}
            try {
                ${'$'}psi = New-Object System.Diagnostics.ProcessStartInfo
                ${'$'}psi.FileName = ${ps(core.first())}
                ${'$'}psi.Arguments = ${ps(args)}
                ${'$'}psi.WorkingDirectory = ${ps(dir.absolutePath)}
                ${'$'}psi.UseShellExecute = ${'$'}false
                ${'$'}psi.CreateNoWindow = ${'$'}true
                ${'$'}psi.RedirectStandardOutput = ${'$'}true
                ${'$'}psi.RedirectStandardError = ${'$'}true
                ${'$'}p = [System.Diagnostics.Process]::Start(${'$'}psi)
            } catch {
                ${'$'}m = [System.Text.Encoding]::UTF8.GetBytes("fatal: " + ${'$'}_.Exception.Message + "`n")
                ${'$'}e.Write(${'$'}m, 0, ${'$'}m.Length)
                ${'$'}e.Close(); ${'$'}o.Close()
                exit 1
            }
            ${'$'}copyOut = ${'$'}p.StandardOutput.BaseStream.CopyToAsync(${'$'}o)
            ${'$'}copyErr = ${'$'}p.StandardError.BaseStream.CopyToAsync(${'$'}e)
            while (-not ${'$'}p.HasExited) {
                if ((Test-Path -LiteralPath ${ps(stop.absolutePath)}) -or -not (Get-Process -Id $appPid -ErrorAction SilentlyContinue)) {
                    try { ${'$'}p.Kill() } catch {}
                    break
                }
                Start-Sleep -Milliseconds 300
            }
            ${'$'}p.WaitForExit()
            [void]${'$'}copyOut.Wait(5000)
            [void]${'$'}copyErr.Wait(5000)
            ${'$'}o.Close(); ${'$'}e.Close()
            exit ${'$'}p.ExitCode
        """.trimIndent()
    }

    /** Whether the launcher's output says the UAC prompt was declined. */
    fun cancelled(line: String): Boolean = line.contains(DECLINED)

    /** One argument as CommandLineToArgvW reads it back. */
    internal fun argv(s: String): String {
        if (s.isNotEmpty() && s.none { it == ' ' || it == '\t' || it == '"' }) return s
        val sb = StringBuilder("\"")
        var slashes = 0
        for (c in s) {
            when (c) {
                '\\' -> slashes++
                '"' -> {
                    sb.append("\\".repeat(slashes * 2 + 1)).append('"')
                    slashes = 0
                }
                else -> {
                    sb.append("\\".repeat(slashes)).append(c)
                    slashes = 0
                }
            }
        }
        return sb.append("\\".repeat(slashes * 2)).append('"').toString()
    }

    /** A PowerShell single-quoted literal; PowerShell takes the typographic quotes for ' too. */
    internal fun ps(s: String): String = "'" + s.replace(Regex("['‘’‚‛]")) { it.value + it.value } + "'"

    /** -EncodedCommand: the script as UTF-16LE in Base64, so no quoting reaches the command line. */
    internal fun encoded(script: String): String = Base64.getEncoder().encodeToString(script.toByteArray(Charsets.UTF_16LE))

    private const val PS_FLAGS = "-NoProfile -NonInteractive -ExecutionPolicy Bypass"
}
