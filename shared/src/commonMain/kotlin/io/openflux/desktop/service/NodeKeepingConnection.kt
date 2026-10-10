package io.openflux.desktop.service

import io.openflux.desktop.model.LogLevel
import io.openflux.desktop.model.Profile
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout

/**
 * Wraps the platform's connection service so that connecting a profile made by
 * the "без сервера" wizard first asks its node on the hosting to run (the core's
 * `start` does nothing when it already does). It is best effort, in the
 * background and never delays the connection: from a network where only the
 * channel opens, the hosting cannot be reached directly, and then the node is
 * simply whatever it already is (it renews itself while it is used).
 */
class NodeKeepingConnection(
    private val inner: ConnectionService,
    private val hosting: PhpHostingService,
    private val scope: CoroutineScope,
) : ConnectionService by inner {

    override fun connect(profile: Profile) {
        val node = profile.phpNode
        if (profile.stream && node != null) {
            scope.launch {
                try {
                    withTimeout(TRY_MS) {
                        hosting.start(node.siteUrl, node.token, profile.transport.cliName, profile.value.trim(), chain = true, quiet = true)
                    }
                    hosting.note("нода на хостинге ${node.siteUrl} работает", LogLevel.Debug)
                } catch (e: TimeoutCancellationException) {
                    hosting.note("нода на хостинге ${node.siteUrl} не ответила за ${TRY_MS / 1000} с: подключаюсь как есть", LogLevel.Debug)
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    hosting.note("хостинг ${node.siteUrl} напрямую недоступен: подключаюсь как есть", LogLevel.Debug)
                }
            }
        }
        inner.connect(profile)
    }

    companion object {
        private const val TRY_MS = 40_000L
    }
}
