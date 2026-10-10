package io.openflux.desktop.model

/** Paths the core gets for one run. */
data class CorePaths(
    val keyFile: String?,
    val confFile: String,
    val cookieStore: String,
    val ipcSocket: String?,
)

/** How to start the core for a profile: the .conf body (Session) and flags. */
data class CoreLaunch(
    val arguments: List<String>,
    val conf: String?,
    val socksAddress: String?,
    val httpProxyAddress: String?,
    /** The core's IPC status says whether it reaches the exit (Sessions); otherwise its log does. */
    val usesIpc: Boolean,
)

/**
 * Turns a profile and the settings into the core's command line. Sessions
 * go through a `--config` file (the only way to name several transports of
 * one type); classic profiles use the single-transport flags.
 */
object CoreConfig {
    const val LOOPBACK = "127.0.0.1"

    fun build(profile: Profile, settings: AppSettings, paths: CorePaths): CoreLaunch {
        val problems = profile.problems()
        require(problems.isEmpty()) { problems.first() }
        val exit = settings.mode == ConnectionMode.Exit
        val socks = "$LOOPBACK:${settings.socksPort}"
        val http = "$LOOPBACK:${settings.socksPort + 1}"
        if (profile.stream) {
            require(!exit) { "Режим без сервера работает только как клиент: выхода в нём нет, сервер заменяет PHP-хостинг" }
            return stream(profile, settings, paths, socks, http)
        }
        return if (profile.session) session(profile, settings, paths, exit, socks, http) else classic(profile, settings, paths, exit, socks, http)
    }

    private fun session(profile: Profile, settings: AppSettings, paths: CorePaths, exit: Boolean, socks: String, http: String): CoreLaunch {
        val conf = buildString {
            appendLine("# OpenFlux Desktop: ${profile.name}")
            appendLine("[Interface]")
            if (exit) {
                appendLine("Role = exit")
                appendLine("Mode = l4")
            } else if (settings.fullTunnel) {
                appendLine("Role = client")
                appendLine("Inbound = tun")
            } else {
                appendLine("Role = client")
                appendLine("Inbound = socks5")
                appendLine("Socks5 = $socks")
            }
            appendLine("EncryptionKeyFile = ${confValue(paths.keyFile ?: error("Session requires a key"))}")
            appendLine("CookieStore = ${confValue(paths.cookieStore)}")
            val specs = profile.sessionSpecs().filterNot { exit && it.type == TransportType.DIRECT }
            for (spec in specs) {
                appendLine()
                appendLine("[Transport ${spec.name}]")
                appendLine("Type = ${spec.type.cliName}")
                appendLine("Priority = ${spec.priority}")
                when (spec.type.kind) {
                    ValueKind.DocumentUrl -> appendLine("URL = ${confValue(spec.value)}")
                    ValueKind.Address -> appendLine("Dial = ${confValue(spec.value)}")
                    ValueKind.Token -> {
                        appendLine("Token = ${confValue(spec.value)}")
                        appendLine("UID = ${confValue(spec.uid)}")
                    }
                }
            }
            // The exit listens for direct only when the profile has it; its
            // address there is what clients dial, the exit takes the port
            // from the settings.
            val direct = profile.carriers.firstOrNull { it.type == TransportType.DIRECT }
            if (exit && direct != null) {
                appendLine()
                appendLine("[Transport direct]")
                appendLine("Type = direct")
                appendLine("Priority = ${direct.priority}")
                appendLine("Listen = 0.0.0.0:${settings.exitDirectPort}")
            }
        }
        val args = buildList {
            add("--config"); add(paths.confFile)
            // An imported context goes on the command line (.conf values end
            // at '#'); without one the core derives it by its rule, the one
            // the exit uses too.
            if (profile.context.isNotBlank()) add("--session-context=${profile.context}")
            if (paths.ipcSocket != null) add("--ipc-socket=${paths.ipcSocket}")
            if (exit) {
                add("--share")
                if (settings.exitShareHost.isNotBlank()) add("--share-host=${settings.exitShareHost.trim()}")
            } else if (!settings.fullTunnel) {
                add("--http-proxy=$http")
            }
            if (settings.debugLevel > 0) add("--debug=${settings.debugLevel}")
        }
        val proxies = !exit && !settings.fullTunnel
        return CoreLaunch(args, conf, if (proxies) socks else null, if (proxies) http else null, usesIpc = paths.ipcSocket != null)
    }

    /**
     * The mode without a server: the core's `--mode=stream` (the stream mux over cups.online or a
     * Mail.ru document to the PHP node), as proxies or, with the full tunnel, through the
     * system's TUN. No key, no codec, no Session: nothing of those applies.
     */
    private fun stream(profile: Profile, settings: AppSettings, paths: CorePaths, socks: String, http: String): CoreLaunch {
        val args = buildList {
            add("--role=client")
            add("--mode=stream")
            if (settings.fullTunnel) {
                add("--inbound=tun")
            } else {
                // Named, not left to the core: core 0.3.0 takes utun for a client on macOS when --inbound is missing.
                add("--inbound=socks5")
                add("--socks5=$socks")
                add("--http-proxy=$http")
            }
            add("--transport=${profile.transport.cliName}")
            add("--url=${profile.value.trim()}")
            // Traffic totals for the speed counters (proxy mode; the log says when it is up).
            if (paths.ipcSocket != null && !settings.fullTunnel) add("--ipc-socket=${paths.ipcSocket}")
            if (settings.debugLevel > 0) add("--debug=${settings.debugLevel}")
        }
        return if (settings.fullTunnel) CoreLaunch(args, null, null, null, usesIpc = false)
        else CoreLaunch(args, null, socks, http, usesIpc = false)
    }

    private fun classic(profile: Profile, settings: AppSettings, paths: CorePaths, exit: Boolean, socks: String, http: String): CoreLaunch {
        val args = buildList {
            if (exit) {
                // The same l4 exit a Session profile runs, serving classic
                // clients of this transport.
                add("--role=exit")
                add("--mode=l4")
            } else if (settings.fullTunnel) {
                add("--role=client")
                add("--inbound=tun")
            } else {
                add("--role=client")
                add("--inbound=socks5")
                add("--socks5=$socks")
                add("--http-proxy=$http")
            }
            add("--transport=${profile.transport.cliName}")
            add("--codec=${profile.codec.cliName}")
            if (profile.transport == TransportType.ONEME) {
                add("--maxToken=${profile.value.trim()}")
                add("--maxUid=${profile.uid.trim()}")
            } else {
                add("--url=${profile.value.trim()}")
            }
            if (paths.keyFile != null) add("--encryption-key-file=${paths.keyFile}")
            add("--cookie-store=${paths.cookieStore}")
            // Traffic totals for the speed counters; the state still comes
            // from the log (usesIpc = false).
            if (paths.ipcSocket != null) add("--ipc-socket=${paths.ipcSocket}")
            if (exit) {
                add("--share")
                if (settings.exitShareHost.isNotBlank()) add("--share-host=${settings.exitShareHost.trim()}")
            }
            if (settings.debugLevel > 0) add("--debug=${settings.debugLevel}")
        }
        return if (exit || settings.fullTunnel) CoreLaunch(args, null, null, null, usesIpc = false)
        else CoreLaunch(args, null, socks, http, usesIpc = false)
    }

    /** .conf values end at '#' or ';' (comments); refuse ones that would be cut. */
    private fun confValue(value: String): String {
        require(value.none { it == '#' || it == ';' || it == '\n' || it == '\r' }) {
            "Значение «${value.take(40)}» нельзя передать ядру: в нём есть # или ;"
        }
        return value
    }
}
