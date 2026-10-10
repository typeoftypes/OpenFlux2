package io.openflux.desktop

import io.openflux.desktop.core.CliCoreLinks
import io.openflux.desktop.core.CoreBinary
import io.openflux.desktop.data.AppDirs
import io.openflux.desktop.core.CoreConnectionService
import io.openflux.desktop.data.FileProfileRepository
import io.openflux.desktop.data.FileSettingsRepository
import io.openflux.desktop.model.CoreShareLinkCodec
import io.openflux.desktop.node.CoreNodeWizard
import io.openflux.desktop.node.CorePhpTransport
import io.openflux.desktop.platform.JvmPlatformServices
import io.openflux.desktop.service.AppContainer
import io.openflux.desktop.service.NodeKeepingConnection
import io.openflux.desktop.service.PhpHostingService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/** Wires the desktop implementations together; called once from main. */
fun createAppContainer(appVersion: String): AppContainer {
    val settings = FileSettingsRepository(AppDirs.config)
    val binary = CoreBinary()
    val phpHosting = PhpHostingService(CorePhpTransport(settings, binary), clock = System::currentTimeMillis)
    return AppContainer(
        profiles = FileProfileRepository(AppDirs.config),
        settings = settings,
        // Connecting a profile made by the "без сервера" wizard first asks its node on the hosting to run.
        connection = NodeKeepingConnection(
            CoreConnectionService(settings, binary), phpHosting, CoroutineScope(SupervisorJob() + Dispatchers.Default),
        ),
        platform = JvmPlatformServices(appVersion) { binary.version() },
        shareCodec = CoreShareLinkCodec(CliCoreLinks(settings, binary)),
        nodeWizard = CoreNodeWizard(settings, binary),
        phpHosting = phpHosting,
    )
}
