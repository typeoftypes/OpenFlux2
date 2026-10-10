package io.openflux.desktop.model

/**
 * What the user reads for each reason the core gives about a link (its
 * share.Code* constants). The core decides which problem it is; the words
 * are the app's.
 */
object ShareLinkMessages {
    /** Every reason the core reports, for the tests that keep this list whole. */
    val codes = listOf(
        "not_link", "unsupported_version", "case_changed", "damaged", "too_large", "bad_payload", "bad_config",
        "no_transports", "several_need_session", "session_secret", "short_secret", "unknown_codec",
        "not_shareable", "unknown_transport", "direct_no_dial", "direct_needs_session",
        "unknown_mode", "stream_transport", "stream_one_transport", "stream_plain_only",
    )

    fun text(code: String, param: String = "", detail: String = ""): String = when (code) {
        "not_link" -> "Это не ссылка openflux://"
        "unsupported_version" -> "Неподдерживаемая версия ссылки, обновите OpenFlux"
        "case_changed" -> "Буквы в ссылке поменяли регистр по дороге: скопируйте её ещё раз"
        "damaged" -> "Ссылка повреждена или обрезана: скопируйте её целиком ещё раз"
        "too_large" -> "Ссылка слишком большая"
        "bad_payload" -> "Ссылка повреждена: внутри не настройки OpenFlux"
        "bad_config" -> "Не удалось собрать ссылку из профиля"
        "no_transports" -> "В ссылке нет транспортов"
        "several_need_session" -> "Несколько транспортов требуют режима Session"
        "session_secret" -> "Для режима Session нужен ключ не короче $param символов"
        "short_secret" -> "Ключ должен быть не короче $param символов"
        "unknown_codec" -> "Неизвестный кодек «$param»"
        "not_shareable" -> "${TransportType.fromCli(param)?.label ?: param} нельзя передать ссылкой: токен привязан к аккаунту"
        "unknown_transport" -> "Неизвестный транспорт «$param»: возможно, нужно обновить OpenFlux"
        "direct_no_dial" -> "У direct нет адреса ноды"
        "direct_needs_session" -> "Direct работает только в режиме Session"
        "unknown_mode" -> "Ссылка для режима «$param», которого это приложение не знает: обновите OpenFlux"
        "stream_transport" -> "Режим без сервера работает через cups.online или Mail.ru, а не через ${TransportType.fromCli(param)?.label ?: param}"
        "stream_one_transport" -> "В режиме без сервера нужен ровно один транспорт"
        "stream_plain_only" -> "В режиме без сервера нет ключа и режима Session"
        else -> if (detail.isNotBlank()) "Не удалось обработать ссылку: $detail" else "Не удалось обработать ссылку"
    }
}
