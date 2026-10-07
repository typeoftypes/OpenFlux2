package io.openflux.app;

import android.app.Activity;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.graphics.Insets;
import android.os.Build;
import android.os.Bundle;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.webkit.CookieManager;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

import androidx.webkit.ProxyConfig;
import androidx.webkit.ProxyController;
import androidx.webkit.WebViewFeature;

import java.io.File;
import java.util.function.BooleanSupplier;

import io.openflux.bridge.mobile.Mobile;

// Lets the user pass a Yandex SmartCaptcha (or log in) when the transport
// core can't get through on its own, then hands the resulting cookies back
// to it. Android's side of the core's out-of-band cookie flow - desktop and
// iOS do the same over transport/ipc.
public final class CaptchaActivity extends Activity {
    // Same UA the Go transport uses for the document fetch: the captcha pass
    // may be bound to it, so solving under a different one could be useless.
    private static final String USER_AGENT =
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:153.0) Gecko/20100101 Firefox/153.0";
    private static final String CHANNEL_ID = "openflux_captcha";
    private static final int NOTIFICATION_ID = 9;

    private static volatile boolean solved;

    private String startUrl;
    // Set when the check belongs to the exit node: the page must load through
    // this proxy (the tunnel) so it is passed from the node's address.
    private String proxy = "";
    private boolean proxyOverridden;
    private String currentUrl;
    private boolean submitted;

    static void initCookieStore(Context context) {
        Mobile.setCookieStorePath(new File(context.getFilesDir(), "transport-cookies.json").getPath());
    }

    // Blocks the calling worker while the core waits for a solve, surfacing
    // it as a notification. Returns true once the user submitted cookies,
    // false if nothing was pending, the user cancelled, or the session ended.
    static boolean awaitIfPending(Context context, BooleanSupplier stillCurrent) {
        if (Mobile.pendingCaptchaURL().isEmpty()) return false;
        solved = false;
        NotificationManager manager = context.getSystemService(NotificationManager.class);
        manager.createNotificationChannel(new NotificationChannel(
                CHANNEL_ID, "Проверка Яндекса", NotificationManager.IMPORTANCE_HIGH));
        Intent open = new Intent(context, CaptchaActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        PendingIntent content = PendingIntent.getActivity(
                context, 1, open, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        boolean login = "login".equals(Mobile.pendingCaptchaReason());
        boolean remote = !Mobile.pendingCaptchaProxy().isEmpty();
        String title = remote
                ? (login ? "OpenFlux: ноде нужен вход в Яндекс" : "OpenFlux: нода просит пройти проверку")
                : (login ? "OpenFlux: нужен вход в Яндекс" : "OpenFlux: нужна проверка");
        manager.notify(NOTIFICATION_ID, new Notification.Builder(context, CHANNEL_ID)
                .setContentTitle(title)
                .setContentText(remote ? "Нажмите, чтобы открыть проверку для ноды"
                        : "Нажмите, чтобы продолжить подключение")
                .setSmallIcon(R.drawable.ic_openflux_notification)
                .setAutoCancel(true)
                .setContentIntent(content)
                .build());
        try {
            while (stillCurrent.getAsBoolean() && !Mobile.pendingCaptchaURL().isEmpty()) {
                Thread.sleep(500);
            }
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        } finally {
            manager.cancel(NOTIFICATION_ID);
        }
        return solved && stillCurrent.getAsBoolean();
    }

    @Override protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        startUrl = Mobile.pendingCaptchaURL();
        if (startUrl.isEmpty()) {
            finish();
            return;
        }
        boolean login = "login".equals(Mobile.pendingCaptchaReason());
        proxy = Mobile.pendingCaptchaProxy();
        boolean remote = !proxy.isEmpty();

        TextView title = new TextView(this);
        title.setText(remote
                ? (login ? "Вход в Яндекс для ноды" : "Проверка для ноды")
                : (login ? "Войдите в Яндекс" : "Пройдите проверку"));
        title.setTextSize(16);
        Button done = new Button(this);
        done.setText("Готово");
        done.setOnClickListener(v -> submit());
        Button cancel = new Button(this);
        cancel.setText("Отмена");
        cancel.setOnClickListener(v -> cancel());

        LinearLayout bar = new LinearLayout(this);
        bar.setOrientation(LinearLayout.HORIZONTAL);
        int pad = Math.round(8 * getResources().getDisplayMetrics().density);
        bar.setPadding(pad * 2, pad, pad, pad);
        bar.addView(title, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        bar.addView(cancel);
        bar.addView(done);

        WebView web = new WebView(this);
        WebSettings settings = web.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setUserAgentString(USER_AGENT);
        CookieManager cookies = CookieManager.getInstance();
        cookies.setAcceptCookie(true);
        cookies.setAcceptThirdPartyCookies(web, true);
        web.setWebViewClient(new WebViewClient() {
            @Override public void onPageFinished(WebView view, String url) {
                currentUrl = url;
                // A real browser is often let through without any check (the
                // captcha targets the transport's bot-like client), so any
                // regular page counts as passed. The short delay lets a page
                // that bounces to a check get there before we submit.
                if (!isCheckpoint(url)) {
                    view.postDelayed(() -> {
                        if (!isCheckpoint(currentUrl)) submit();
                    }, 1500);
                }
            }
        });

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.addView(bar);
        root.addView(web, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        // Keep the bar and the page clear of the status/navigation bars and
        // the keyboard (edge-to-edge is enforced from Android 15).
        if (Build.VERSION.SDK_INT >= 30) {
            root.setOnApplyWindowInsetsListener((v, insets) -> {
                Insets bars = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.ime());
                v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
                return WindowInsets.CONSUMED;
            });
        } else {
            root.setFitsSystemWindows(true);
        }
        setContentView(root);
        if (!remote) {
            web.loadUrl(startUrl);
            return;
        }
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)) {
            Toast.makeText(this, "WebView не умеет работать через прокси: обновите Android System WebView",
                    Toast.LENGTH_LONG).show();
            cancel();
            return;
        }
        ProxyConfig config = new ProxyConfig.Builder().addProxyRule(proxy).build();
        proxyOverridden = true;
        ProxyController.getInstance().setProxyOverride(config, Runnable::run, () -> web.loadUrl(startUrl));
    }

    @Override protected void onDestroy() {
        // The override applies to every WebView in the process; drop it.
        if (proxyOverridden) ProxyController.getInstance().clearProxyOverride(Runnable::run, () -> { });
        super.onDestroy();
    }

    @Override public void onBackPressed() {
        cancel();
    }

    private static boolean isCheckpoint(String url) {
        return url != null && (url.contains("showcaptcha") || url.contains("passport.yandex"));
    }

    private void submit() {
        if (submitted) return;
        CookieManager manager = CookieManager.getInstance();
        manager.flush();
        StringBuilder header = new StringBuilder();
        for (String url : new String[] {startUrl, currentUrl}) {
            String part = url == null ? null : manager.getCookie(url);
            if (part == null || part.isEmpty()) continue;
            if (header.length() > 0) header.append("; ");
            header.append(part);
        }
        String cookieHeader = header.toString();
        Mobile.setInitialCookies(cookieHeader);
        String error = Mobile.submitCaptchaCookies(cookieHeader);
        if (error != null && !error.isEmpty()) {
            Toast.makeText(this, error, Toast.LENGTH_LONG).show();
            return;
        }
        submitted = true;
        solved = true;
        finish();
    }

    private void cancel() {
        if (!submitted) Mobile.cancelCaptcha();
        finish();
    }
}
