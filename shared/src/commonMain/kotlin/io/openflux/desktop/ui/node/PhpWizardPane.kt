package io.openflux.desktop.ui.node

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.OpenInNew
import androidx.compose.material.icons.rounded.QrCode2
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import io.openflux.desktop.model.PhpHosts
import io.openflux.desktop.model.PhpMessages
import io.openflux.desktop.model.TransportType
import io.openflux.desktop.ui.LocalScrollbars
import io.openflux.desktop.ui.LocalTouchUi
import io.openflux.desktop.ui.PlatformBackHandler
import io.openflux.desktop.ui.components.AppButton
import io.openflux.desktop.ui.components.AppCard
import io.openflux.desktop.ui.components.AppDialog
import io.openflux.desktop.ui.components.AppIconButton
import io.openflux.desktop.ui.components.AppIcons
import io.openflux.desktop.ui.components.AppTextField
import io.openflux.desktop.ui.components.Banner
import io.openflux.desktop.ui.components.ButtonStyle
import io.openflux.desktop.ui.components.HorizontalRule
import io.openflux.desktop.ui.components.KeyValueRow
import io.openflux.desktop.ui.components.LocalToaster
import io.openflux.desktop.ui.components.QrCode
import io.openflux.desktop.ui.components.SectionLabel
import io.openflux.desktop.ui.components.Segmented
import io.openflux.desktop.ui.components.SwitchRow
import io.openflux.desktop.ui.components.TextAction
import io.openflux.desktop.ui.components.Tone
import io.openflux.desktop.ui.theme.AppTheme

/**
 * "Без сервера" in the Profiles section: the exit is a small PHP program on
 * an ordinary web hosting, put there over FTP. [onClose] leaves it; [onSaved]
 * opens the saved profile.
 */
@Composable
fun PhpWizardPane(model: PhpWizardModel, onClose: () -> Unit, onSaved: (String) -> Unit) {
    var confirmClose by remember { mutableStateOf(false) }
    var qrOpen by remember { mutableStateOf(false) }
    val requestClose = {
        if (model.busy == null) {
            if (model.unsaved) confirmClose = true else onClose()
        }
    }
    PlatformBackHandler(enabled = LocalTouchUi.current && model.busy == null) {
        if (model.step == PhpStep.Channel || (model.step == PhpStep.Install && model.installed == null)) model.back() else requestClose()
    }
    val scroll = rememberScrollState()
    val scrollbars = LocalScrollbars.current
    Box(Modifier.fillMaxSize()) {
        Column(Modifier.fillMaxSize()) {
            val progress = model.progress
            when {
                model.busy != null && progress != null && progress.phase == "upload" ->
                    LinearProgressIndicator(progress = { progress.fraction }, modifier = Modifier.fillMaxWidth().height(3.dp), color = AppTheme.colors.accent)
                model.busy != null -> LinearProgressIndicator(Modifier.fillMaxWidth().height(3.dp), color = AppTheme.colors.accent)
                else -> Spacer(Modifier.height(3.dp))
            }
            Column(
                Modifier.fillMaxSize().verticalScroll(scroll).padding(horizontal = AppTheme.spacing.page, vertical = AppTheme.spacing.xl),
            ) {
                Column(Modifier.widthIn(max = 640.dp)) {
                    Header(model, requestClose)
                    Spacer(Modifier.height(AppTheme.spacing.xl))
                    when (model.step) {
                        PhpStep.Hosting -> HostingStep(model)
                        PhpStep.Channel -> ChannelStep(model)
                        PhpStep.Install -> InstallStep(model)
                        PhpStep.Verify -> VerifyStep(model)
                        PhpStep.Done -> DoneStep(model, onShowQr = { qrOpen = true }, onSaved = onSaved, onClose = requestClose)
                    }
                    Status(model)
                }
            }
        }
        scrollbars.Vertical(scroll, Modifier.align(Alignment.CenterEnd))
    }

    if (confirmClose) {
        AppDialog(
            title = "Выйти без сохранения?",
            onDismiss = { confirmClose = false },
            primary = "Сохранить профиль",
            onPrimary = {
                confirmClose = false
                model.save()?.let { onSaved(it.id) } ?: onClose()
            },
            secondary = "Остаться",
        ) {
            Text(
                "Нода уже лежит на хостинге, но профиль не сохранён: без него к ней не подключиться. " +
                    "Можно и убрать файлы с хостинга.",
                style = AppTheme.typography.body, color = AppTheme.colors.text,
            )
            Spacer(Modifier.height(AppTheme.spacing.m))
            TextAction("Выйти, не сохраняя", { confirmClose = false; onClose() }, color = AppTheme.colors.danger)
        }
    }
    if (qrOpen) {
        AppDialog(title = "Отсканируйте в OpenFlux", onDismiss = { qrOpen = false }, secondary = "Закрыть") {
            val matrix = remember(model.shareLink) { model.qr() }
            val hint = "На другом устройстве: Профили → Импорт → Сканировать QR. В коде нет ключей: режим без сервера их не использует. " +
                "Код даёт только подключение, управлять нодой на хостинге с него нельзя."
            BoxWithConstraints(Modifier.fillMaxWidth()) {
                val width = maxWidth
                if (width >= 440.dp) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        QrCode(matrix, 220.dp)
                        Spacer(Modifier.width(AppTheme.spacing.l))
                        Text(hint, style = AppTheme.typography.body, color = AppTheme.colors.textSecondary)
                    }
                } else {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        QrCode(matrix, minOf(width, 260.dp))
                        Spacer(Modifier.height(AppTheme.spacing.l))
                        Text(hint, style = AppTheme.typography.body, color = AppTheme.colors.textSecondary)
                    }
                }
            }
        }
    }
}

@Composable
private fun Header(model: PhpWizardModel, onClose: () -> Unit) {
    val (title, subtitle) = when (model.step) {
        PhpStep.Hosting -> "Без своего сервера" to "Нода встанет на любой хостинг с PHP и FTP."
        PhpStep.Channel -> "Канал связи" to "Через что устройство и нода на хостинге будут находить друг друга."
        PhpStep.Install -> if (model.existing) "Подключение ноды" to "Мастер проверит ноду на сайте и запустит её."
            else "Установка на хостинг" to "Мастер зальёт файлы ноды и запустит её."
        PhpStep.Verify -> "Проверка" to "Подключаюсь через новую ноду и открываю сайт."
        PhpStep.Done -> "Нода готова" to if (model.verifiedIp.isEmpty()) "Нода установлена, но проверка не завершена."
            else "Трафик выходит в интернет с адреса ${model.verifiedIp}."
    }
    Row(verticalAlignment = Alignment.Top) {
        if (model.step == PhpStep.Channel || (model.step == PhpStep.Install && model.installed == null)) {
            AppIconButton("Назад", model::back, resource = AppIcons.Back, enabled = model.busy == null)
            Spacer(Modifier.width(AppTheme.spacing.s))
        }
        Column(Modifier.weight(1f)) {
            Text("Шаг ${model.step.number} из 4", style = AppTheme.typography.label, color = AppTheme.colors.accent)
            Spacer(Modifier.height(AppTheme.spacing.xs))
            Text(title, style = AppTheme.typography.pageTitle, color = AppTheme.colors.text)
            Text(subtitle, style = AppTheme.typography.body, color = AppTheme.colors.textSecondary)
        }
        AppButton("Закрыть", onClose, style = ButtonStyle.Ghost, enabled = model.busy == null)
    }
}

@Composable
private fun Status(model: PhpWizardModel) {
    val busy = model.busy
    val error = model.error
    val notice = model.notice
    if (busy == null && error == null && notice == null) return
    Spacer(Modifier.height(AppTheme.spacing.l))
    when {
        busy != null -> Banner(busy, Tone.Accent, icon = Icons.Rounded.Info)
        error != null -> Banner(error, Tone.Danger, icon = Icons.Rounded.ErrorOutline)
        notice != null -> Banner(notice, Tone.Success, icon = Icons.Rounded.CheckCircle)
    }
}

@Composable
private fun Note(text: String) {
    Spacer(Modifier.height(AppTheme.spacing.s))
    Text(text, style = AppTheme.typography.bodySmall, color = AppTheme.colors.textSecondary)
}

@Composable
private fun Actions(content: @Composable () -> Unit) {
    Spacer(Modifier.height(AppTheme.spacing.xl))
    FlowRow(horizontalArrangement = Arrangement.spacedBy(AppTheme.spacing.s), verticalArrangement = Arrangement.spacedBy(AppTheme.spacing.s)) {
        content()
    }
}

private const val PARSER_SKIPPED = "Хостинг не принял файл со считывателем ссылок для страницы ноды. Нода работает и без него, " +
    "только её страница в браузере не покажет ссылку и QR: их даёт приложение."

// ---- step 1 ----

@Composable
private fun ColumnScope.HostingStep(model: PhpWizardModel) {
    val idle = model.busy == null
    var advanced by remember { mutableStateOf(false) }
    Segmented(listOf(false, true), model.existing, { if (it) "Нода уже залита" else "Залить по FTP" },
        { model.existing = it; model.error = null }, enabled = idle)
    Spacer(Modifier.height(AppTheme.spacing.l))
    if (model.existing) { ExistingNodeFields(model); return }
    Banner(
        "Подойдёт любой хостинг: бесплатный или платный, российский или зарубежный, лишь бы на нём работал PHP и был доступ по FTP или FTPS " +
            "(SFTP по SSH не подходит). Понадобятся адрес FTP-сервера, логин и пароль от FTP и адрес вашего сайта на этом хостинге.",
        Tone.Neutral, icon = Icons.Rounded.Info,
    )
    Spacer(Modifier.height(AppTheme.spacing.l))
    SectionLabel("Ваш хостинг")
    Note(
        "FTP-данные лежат в панели хостинга (раздел «FTP», «FTP-аккаунты», «Доступ по FTP») или в письме при регистрации. " +
            "Адрес сервера обычно вида ftp.ваш-сайт.ru или указан в панели.",
    )
    Spacer(Modifier.height(AppTheme.spacing.m))
    AppTextField(model.ftpHost, { model.ftpHost = it.trim() }, label = "Адрес FTP-сервера", placeholder = "ftp.example.com", enabled = idle)
    if (PhpHosts.presets.isNotEmpty()) {
        Spacer(Modifier.height(AppTheme.spacing.xs))
        FlowRow(horizontalArrangement = Arrangement.spacedBy(AppTheme.spacing.s), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("Подставить адрес:", style = AppTheme.typography.caption, color = AppTheme.colors.textHint, modifier = Modifier.padding(top = 6.dp))
            PhpHosts.presets.forEachIndexed { index, preset ->
                TextAction(preset.title, { model.usePreset(index) })
            }
        }
    }
    model.preset?.let { Note(it.note) }
    Spacer(Modifier.height(AppTheme.spacing.m))
    Row(horizontalArrangement = Arrangement.spacedBy(AppTheme.spacing.m)) {
        AppTextField(model.ftpUser, { model.ftpUser = it.trim() }, Modifier.weight(1f), label = "Логин FTP",
            placeholder = model.preset?.userHint.orEmpty(), enabled = idle)
        AppTextField(model.ftpPassword, { model.ftpPassword = it }, Modifier.weight(1f), label = "Пароль FTP", secret = true, enabled = idle)
    }
    Spacer(Modifier.height(AppTheme.spacing.m))
    AppTextField(model.siteInput, { model.siteInput = it.trim() }, label = "Адрес вашего сайта на хостинге",
        placeholder = "https://ваш-сайт.ru", enabled = idle)
    Note("Пароль нужен только на время установки: OpenFlux его не сохраняет.")

    if (model.folderChoices.isNotEmpty()) {
        Spacer(Modifier.height(AppTheme.spacing.l))
        SectionLabel("Какая папка отдаётся как сайт?")
        Spacer(Modifier.height(AppTheme.spacing.s))
        FlowRow(horizontalArrangement = Arrangement.spacedBy(AppTheme.spacing.s), verticalArrangement = Arrangement.spacedBy(AppTheme.spacing.s)) {
            model.folderChoices.forEach { dir -> AppButton(dir, { model.chooseFolder(dir) }, style = ButtonStyle.Secondary, enabled = idle) }
        }
    }

    Spacer(Modifier.height(AppTheme.spacing.l))
    TextAction(if (advanced) "Скрыть дополнительное" else "Дополнительно: порт, шифрование, папка, свой ключ", { advanced = !advanced })
    if (advanced) {
        Spacer(Modifier.height(AppTheme.spacing.m))
        Row(horizontalArrangement = Arrangement.spacedBy(AppTheme.spacing.m)) {
            AppTextField(model.ftpPort, { model.ftpPort = it.filter(Char::isDigit).take(5) }, Modifier.width(110.dp), label = "Порт FTP",
                keyboardType = KeyboardType.Number, enabled = idle)
            AppTextField(model.folderInput, { model.folderInput = it.trim() }, Modifier.weight(1f), label = "Папка сайта (если знаете)",
                placeholder = "htdocs", enabled = idle)
        }
        Spacer(Modifier.height(AppTheme.spacing.m))
        AppTextField(model.chosenToken, { model.chosenToken = it.trim() }, label = "Свой ключ доступа (необязательно)",
            placeholder = "оставьте пустым — ключ создастся сам", secret = true, enabled = idle)
        Note(
            "Ключ охраняет страницу и управление нодой. 8–64 знака: латинские буквы, цифры, «-» и «_». " +
                "Если на хостинге уже стоит нода, с новым ключом её прежние адреса перестанут работать.",
        )
        Spacer(Modifier.height(AppTheme.spacing.m))
        AppCard(padding = 0.dp) {
            SwitchRow(
                "Разрешить FTP без шифрования",
                "Некоторые хостинги не умеют защищённый FTP. Тогда пароль пойдёт по сети открытым текстом: после установки смените его.",
                model.ftpTls == "none", { model.ftpTls = if (it) "none" else "auto" }, enabled = idle,
            )
        }
    }
    Actions {
        AppButton(if (idle) "Проверить вход" else "Проверяю…", model::probeHosting, enabled = idle)
    }
}

/** A node already on the hosting (uploaded by hand, or by another device): its site and access key. */
@Composable
private fun ColumnScope.ExistingNodeFields(model: PhpWizardModel) {
    val idle = model.busy == null
    Banner(
        "Файлы ноды уже лежат на хостинге (вы залили их сами или с другого устройства). FTP не нужен: " +
            "достаточно адреса сайта и ключа доступа ноды.",
        Tone.Neutral, icon = Icons.Rounded.Info,
    )
    Spacer(Modifier.height(AppTheme.spacing.l))
    AppTextField(model.siteInput, model::onExistingAddress, label = "Адрес сайта или страницы ноды",
        placeholder = "https://ваш-сайт.ru/mailruexit.php?k=…", enabled = idle)
    Note("Можно вставить адрес страницы ноды целиком: ключ, канал и комната или документ подставятся сами.")
    Spacer(Modifier.height(AppTheme.spacing.m))
    AppTextField(model.tokenInput, { model.tokenInput = it.trim() }, label = "Ключ доступа ноды", secret = true, enabled = idle)
    Note(
        "Ключ стоит в адресе страницы ноды после «k=» и в файле config.php на хостинге (PHPBOX_TOKEN). " +
            "Он даёт управлять нодой: храните его как пароль.",
    )
    Actions {
        AppButton("Далее", model::useExisting, enabled = idle)
    }
}

// ---- step 2 ----

@Composable
private fun ColumnScope.ChannelStep(model: PhpWizardModel) {
    val idle = model.busy == null
    model.probe?.let { probe ->
        AppCard(padding = 0.dp) {
            KeyValueRow("Хостинг", "${model.ftpUser.trim()}@${model.ftpHost.trim()}")
            HorizontalRule()
            KeyValueRow("Папка сайта", probe.dir.ifEmpty { "корень FTP" })
            HorizontalRule()
            KeyValueRow("Сайт", model.siteUrl)
            if (probe.hasNode) {
                HorizontalRule()
                KeyValueRow("Уже стоит нода", "будет обновлена, её адреса сохранятся")
            }
        }
        model.securityNote?.let {
            Spacer(Modifier.height(AppTheme.spacing.m))
            Banner(it, Tone.Warning, icon = Icons.Rounded.Warning)
        }
        Spacer(Modifier.height(AppTheme.spacing.l))
    }
    if (model.existing) {
        AppCard(padding = 0.dp) {
            KeyValueRow("Сайт", model.siteUrl)
            HorizontalRule()
            KeyValueRow("Ключ доступа", PhpHosts.maskToken(model.tokenInput.trim()))
        }
        Spacer(Modifier.height(AppTheme.spacing.l))
    }
    AppTextField(model.name, { model.name = it }, label = "Название профиля", enabled = idle)

    Spacer(Modifier.height(AppTheme.spacing.xl))
    SectionLabel("Канал связи")
    Note(
        "Устройство и нода на хостинге встречаются в общем «месте»: хостингу не нужно принимать подключения, " +
            "а из сетей с ограничениями достаточно, чтобы открывался сам канал.",
    )
    Spacer(Modifier.height(AppTheme.spacing.s))
    Segmented(PhpHosts.carriers, model.carrier, { if (it == TransportType.CUPSONLINE) "Cups.online" else "Mail.ru Документы" },
        { model.carrier = it }, enabled = idle)
    Spacer(Modifier.height(AppTheme.spacing.m))
    when (model.carrier) {
        TransportType.CUPSONLINE -> Banner(
            if (model.knownRoom.isNotEmpty()) "Комната из адреса ноды: ${model.knownRoom.substringAfter("room=")}"
            else "Комнату мастер создаст сам, вводить ничего не нужно. Это самый простой вариант.",
            Tone.Neutral, icon = Icons.Rounded.Info,
        )
        else -> {
            AppTextField(model.mailruInput, { model.mailruInput = it.trim() }, label = "Публичная ссылка на документ Mail.ru",
                placeholder = "https://cloud.mail.ru/public/…", enabled = idle)
            Note(
                "Создайте в Облаке Mail.ru любой документ, откройте доступ по ссылке «Редактирование для всех» и вставьте ссылку сюда.",
            )
        }
    }
    Actions {
        AppButton(if (idle) "Далее" else "Подождите…", model::prepareChannel, enabled = idle)
    }
}

// ---- step 3 ----

@Composable
private fun ColumnScope.InstallStep(model: PhpWizardModel) {
    val idle = model.busy == null
    if (model.existing) {
        AppCard(padding = 0.dp) {
            KeyValueRow("Сайт", model.siteUrl)
            HorizontalRule()
            KeyValueRow("Ключ доступа", PhpHosts.maskToken(model.tokenInput.trim()))
            HorizontalRule()
            KeyValueRow("Канал", if (model.carrier == TransportType.CUPSONLINE) "Cups.online" else "Mail.ru Документы")
        }
        Note("Мастер проверит, что сайт отвечает как нода с этим ключом, запустит её и подключится через неё. Файлы на хостинге он не трогает.")
        Actions {
            AppButton(if (idle) "Проверить и запустить" else "Проверяю…", model::install, enabled = idle)
        }
        return
    }
    AppCard(padding = 0.dp) {
        KeyValueRow("Хостинг", "${model.ftpUser.trim()}@${model.ftpHost.trim()}")
        HorizontalRule()
        KeyValueRow("Сайт", model.siteUrl)
        HorizontalRule()
        KeyValueRow("Канал", if (model.carrier == TransportType.CUPSONLINE) "Cups.online, комната создана" else "Mail.ru Документы")
    }
    Note(
        "В папку сайта будет загружено около 2 МБ: файлы ноды и файл с её ключом доступа. Остальные файлы на хостинге мастер не трогает. " +
            "Потом мастер проверит, что сайт отвечает, запустит ноду и подключится через неё.",
    )
    model.installed?.let { done ->
        if (done.tokenReused) Note("Нода на этом хостинге уже была: её ключ доступа сохранён, прежние адреса продолжают работать.")
        if (done.skipped.isNotEmpty()) Note(PARSER_SKIPPED)
    }
    model.securityNote?.let {
        Spacer(Modifier.height(AppTheme.spacing.m))
        Banner(it, Tone.Warning, icon = Icons.Rounded.Warning)
    }
    Actions {
        AppButton(if (idle) "Установить и запустить" else "Устанавливаю…", model::install, enabled = idle)
    }
}

// ---- step 4 ----

@Composable
private fun ColumnScope.VerifyStep(model: PhpWizardModel) {
    val failed = model.verifyFailed
    if (failed == null) {
        Note("Первый запрос через новую ноду может занять до минуты: ей нужно присоединиться к каналу.")
        return
    }
    Banner(failed, Tone.Danger, icon = Icons.Rounded.ErrorOutline)
    Note(
        "Нода стоит на хостинге, но запрос через неё не прошёл. Подождите минуту и повторите. Если не помогает, откройте адрес сайта " +
            "в браузере: на странице ноды виден её журнал.",
    )
    Actions {
        AppButton("Повторить", model::retryVerify, leading = Icons.Rounded.Refresh, enabled = model.busy == null)
        AppButton("Пропустить проверку", model::skipVerification, style = ButtonStyle.Secondary, enabled = model.busy == null)
    }
}

// ---- done ----

@Composable
private fun ColumnScope.DoneStep(model: PhpWizardModel, onShowQr: () -> Unit, onSaved: (String) -> Unit, onClose: () -> Unit) {
    val toaster = LocalToaster.current
    AppCard(padding = 0.dp) {
        KeyValueRow("Профиль", model.name.trim().ifEmpty { model.profile?.name.orEmpty() })
        HorizontalRule()
        KeyValueRow("Сайт с нодой", model.siteUrl)
        if (model.verifiedIp.isNotEmpty()) {
            HorizontalRule()
            KeyValueRow("Внешний адрес", model.verifiedIp, valueColor = AppTheme.colors.success)
        }
        HorizontalRule()
        KeyValueRow("Нода", model.nodeState?.let(PhpMessages::nodeStatus) ?: if (model.nodeRunning) "работает" else "остановлена")
        if (model.maskedToken.isNotEmpty()) {
            HorizontalRule()
            KeyValueRow("Ключ доступа", model.maskedToken) {
                AppIconButton("Копировать ключ", {
                    model.copyToken()
                    toaster.show("Ключ доступа скопирован", Tone.Success)
                }, icon = Icons.Rounded.ContentCopy)
            }
        }
    }
    Note("Ключ доступа нужен, чтобы добавить эту ноду на другом устройстве (пункт «Нода уже залита») или открыть её страницу. Держите его в секрете.")
    if (model.installed?.skipped?.isNotEmpty() == true) Note(PARSER_SKIPPED)
    Note(
        "Нода сама продлевает себя на хостинге, пока вы ей пользуетесь. Если она остановится, приложение запустит её при подключении, " +
            "когда хостинг доступен напрямую. В сетях, где открыт только канал, запустить её заново нельзя: сделайте это из другой сети.",
    )
    Actions {
        if (model.saved) {
            AppButton("Открыть профиль", { model.profile?.let { onSaved(it.id) } }, leading = Icons.Rounded.CheckCircle)
        } else {
            AppButton("Сохранить на этом устройстве", {
                model.save()?.let { toaster.show("Профиль «${it.name}» сохранён", Tone.Success) }
            })
        }
        AppButton("QR для другого устройства", onShowQr, style = ButtonStyle.Secondary, leading = Icons.Rounded.QrCode2, enabled = model.shareLink.isNotEmpty())
        AppButton("Копировать ссылку", {
            model.copyLink()
            toaster.show("Ссылка скопирована", Tone.Success)
        }, style = ButtonStyle.Secondary, leading = Icons.Rounded.ContentCopy, enabled = model.shareLink.isNotEmpty())
    }
    Spacer(Modifier.height(AppTheme.spacing.m))
    Banner(
        "В ссылке и QR нет ключей, но любой, у кого они есть, сможет выходить в интернет через ваш хостинг: " +
            "передавайте их только тем, кому доверяете.",
        Tone.Neutral, icon = Icons.Rounded.Lock,
    )
    Actions {
        AppButton("Панель ноды", model::openPanel, style = ButtonStyle.Secondary, leading = Icons.Rounded.OpenInNew, enabled = model.busy == null)
        AppButton("Обновить состояние", model::refreshNode, style = ButtonStyle.Secondary, leading = Icons.Rounded.Refresh, enabled = model.busy == null)
    }
    Actions {
        AppButton("Остановить ноду", model::stopNode, style = ButtonStyle.Ghost, leading = Icons.Rounded.Stop, enabled = model.busy == null && model.nodeRunning)
        if (model.canRemove) {
            AppButton("Удалить с хостинга", model::removeFromHosting, style = ButtonStyle.Ghost, leading = Icons.Rounded.Delete, enabled = model.busy == null)
        }
        AppButton("Готово", onClose, style = ButtonStyle.Ghost)
    }
}
