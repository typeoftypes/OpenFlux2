package io.openflux.desktop.service

import androidx.compose.runtime.staticCompositionLocalOf
import io.openflux.desktop.model.AppSettings
import io.openflux.desktop.model.CaptchaPrompt
import io.openflux.desktop.model.ConnectionState
import io.openflux.desktop.model.ExitAddress
import io.openflux.desktop.model.LogLevel
import io.openflux.desktop.model.LogLine
import io.openflux.desktop.model.NewChannel
import io.openflux.desktop.model.NodePlan
import io.openflux.desktop.model.NodeTransport
import io.openflux.desktop.model.ServerProbe
import io.openflux.desktop.model.SshTarget
import io.openflux.desktop.model.Profile
import io.openflux.desktop.model.ShareLinkCodec
import io.openflux.desktop.model.TrafficStats
import io.openflux.desktop.ui.BrowserPage
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/** Saved profiles. Writes are persisted before the flow updates. */
interface ProfileRepository {
    val profiles: StateFlow<List<Profile>>
    fun upsert(profile: Profile)
    fun delete(id: String)
    fun newId(): String
}

interface SettingsRepository {
    val settings: StateFlow<AppSettings>
    fun update(transform: (AppSettings) -> AppSettings)
}

/** Runs the OpenFlux core for one profile at a time. */
interface ConnectionService {
    val state: StateFlow<ConnectionState>
    val traffic: StateFlow<TrafficStats>
    val exitAddress: StateFlow<ExitAddress>
    val logs: StateFlow<List<LogLine>>
    val captcha: StateFlow<CaptchaPrompt?>
    /** Exit mode: the `openflux://` link the core printed for clients. */
    val exitShareLink: StateFlow<String?>
    /** The local SOCKS5 address while connected as a client. */
    val socksAddress: StateFlow<String?>
    /** The Yandex check in the built-in browser while [captcha] is open. */
    val captchaPage: StateFlow<BrowserPage?>

    fun connect(profile: Profile)
    fun disconnect()
    fun refreshExitAddress()
    fun clearLogs()

    fun openCaptcha()
    fun submitCaptcha()
    fun dismissCaptcha()

    /** Stops the core and undoes system changes; called once on app exit. */
    fun shutdown()
}

/** Where the app runs; decides wording and touch-sized controls. */
enum class PlatformKind { Desktop, Android }

/** Platform facilities the UI needs without touching the platform itself. */
interface PlatformServices {
    val kind: PlatformKind get() = PlatformKind.Desktop
    val appVersion: String
    val coreVersion: String
    /** Where this app's own releases are published, e.g. "p1neappleXpress/OpenFluxDesktop". */
    val clientRepo: String
    /** Whether this OS can point its system proxy at OpenFlux. */
    val systemProxySupported: Boolean
    /** Whether the full tunnel (all traffic through Wintun on Windows, utun on macOS) runs on this OS. */
    val fullTunnelSupported: Boolean
    /** Whether OpenFlux runs with administrator rights, which the full tunnel needs. */
    val elevated: Boolean
    /**
     * How the full tunnel gets administrator rights for the core on each
     * connect, in the user's words ("macOS спросит пароль администратора"),
     * when OpenFlux itself is not elevated; null when nothing is asked.
     */
    val fullTunnelPrompt: String? get() = null

    /** Starts OpenFlux again as administrator (UAC) and exits this copy; false if that did not happen. */
    fun restartElevated(): Boolean

    fun clipboardText(): String?
    fun setClipboardText(text: String)
    /** Whether [qrFromClipboardImage] can find images on the clipboard. */
    val clipboardImageSupported: Boolean get() = true
    /** Text of a QR code in the image on the clipboard, null when none. */
    fun qrFromClipboardImage(): String?
    /** Text of a QR code in an image file ([pickFile]'s result). */
    fun qrFromFile(path: String): String?
    /** A file the user picks (a path or, on Android, a content URI); null if they cancel. */
    suspend fun pickFile(title: String, extensions: List<String>): String?
    /** Whether [scanQr] has a camera to use. */
    val cameraScanSupported: Boolean get() = false
    /** Text of a QR code the camera read; null if the user cancels. */
    suspend fun scanQr(): String? = null
    /** A small text file's contents (an SSH key), null if it cannot be read. */
    fun readTextFile(path: String, maxBytes: Int = 64 * 1024): String?
    /** The QR modules of [text], rows of dark (true) cells. */
    fun qrMatrix(text: String): List<BooleanArray>
    fun openUrl(url: String)
    /** A new random channel key (64 hex characters). */
    fun newSecret(): String
    fun now(): Long
    /** Newest app release tag on GitHub, null when unknown. */
    suspend fun latestRelease(): String?
}

/**
 * The "Своя нода" wizard's server side: installs an independent exit
 * channel on the user's VDS over SSH (the core's --node-wizard), creates
 * the channel's Yandex document in the built-in browser and checks it.
 * Calls block until done and fail with NodeWizardException.
 */
interface NodeWizardService {
    /** SSH in, download the pinned installer and look at the server. */
    suspend fun connect(target: SshTarget): ServerProbe
    suspend fun newChannel(): NewChannel
    /**
     * What installing [channel] with [transports] (besides direct) would
     * change; the server picks the port. [autoUpdate] turns the server's
     * core updater on or off.
     */
    suspend fun plan(channel: String, transports: List<NodeTransport>, autoUpdate: Boolean): NodePlan
    /** Install and start the channel. */
    suspend fun apply(
        channel: NewChannel,
        transports: List<NodeTransport>,
        port: Int,
        autoUpdate: Boolean,
        sudoPassword: String,
    )
    suspend fun remove(channel: String, sudoPassword: String)
    /** Whether the node can use the Yandex document (edit by link), as an anonymous visitor. */
    suspend fun checkDocument(documentUrl: String)
    /** New cups.online rooms for the channel: the packed list the node and its link take. */
    suspend fun createCupsRooms(): String
    /** The channel's `openflux://` link: [transports], then direct to host:port as the backup. */
    suspend fun shareLink(name: String, key: String, host: String, port: Int, transports: List<NodeTransport>): String
    /** The addresses [host] resolves to, to compare with the tunnel's exit. */
    suspend fun resolve(host: String): Set<String>

    /** This attempt's trace: every SSH/RPC call and the wizard's own step narration, for the Logs tab. */
    val logs: StateFlow<List<LogLine>>
    fun clearLogs()
    /** Adds a line to [logs] from outside (the wizard model's own step narration). */
    fun note(text: String, level: LogLevel = LogLevel.Info)

    /** Ends the SSH session and the helper process. */
    fun close()
}

/** Everything the UI depends on, built once in main. */
class AppContainer(
    val profiles: ProfileRepository,
    val settings: SettingsRepository,
    val connection: ConnectionService,
    val platform: PlatformServices,
    val shareCodec: ShareLinkCodec,
    val nodeWizard: NodeWizardService,
    /** The "без сервера" wizard's steps: a PHP node on an ordinary web host, put there over FTP by the core. */
    val phpHosting: PhpHostingService = PhpHostingService(),
) {
    /** An `openflux://` link opened from outside (a scanned code, a chat); the Profiles screen imports it. */
    val incomingLink = MutableStateFlow<String?>(null)
}

val LocalAppContainer = staticCompositionLocalOf<AppContainer> { error("AppContainer is not provided") }
