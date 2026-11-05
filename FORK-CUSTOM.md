# FORK-CUSTOM — реестр кастомных изменений форка

Этот файл — список всего, чем форк `Evgenrzz/rospanel-Amn` отличается от upstream
`AppsGanin/rospanel`, и чек-лист переноса при merge новой версии upstream.
Актуальный срез диффа: `git diff --stat v5.14.0..HEAD` (точка последнего порта, ветка `fork-5.14.0`).
После каждого нового merge дополняй разделы и обновляй команду среза.

Правила работы агента, сводный реестр путей и журнал работ — в `AGENTS.md`.
Каждое изменение в форке записывается ОБЯЗАТЕЛЬНО в оба файла (см. `AGENTS.md` §0).

## 0. Правила обновлений (регламент владельца)
- Форк — ЕДИНСТВЕННЫЙ источник обновлений для мастера и нод. Upstream напрямую не ставится никогда.
- Цепочка: upstream релиз → merge тега в форк → bump `internal/version/version.go` (номер держим СТРОГО выше того, что уже на нодах) → push `main` → push тега `v*` → `.github/workflows/fork-release.yml` собирает ассеты (`rospanel-linux-amd64/arm64`, `SHA256SUMS`) → обновляем МАСТЕР → затем ноды.
- Ноды сами НЕ обновляются: steady-state `auto_update_cron=''`, `auto_update_nodes=0`; конвергенция — вручную (swap бинаря с проверкой sha256) или разовым временным кроном с мастера.
- `release-please.yml` выключен тумблером на стороне GitHub (не удалять файл, не включать).
- Перед опорой на пайплайн проверять, что `fork-release.yml` жив на main: force-push однажды снёс его (см. раздел 1).

## 1. Релиз-пайплайн и указатель на форк
- `.github/workflows/fork-release.yml` — релиз по тегу `v*` (web build + go build + SHA256SUMS + gh release create). ВОССТАНОВЛЕН 25.10.2026 после force-push'а main, который его удалил; при любом merge/force-push проверять наличие: `git ls-tree fork/main -- .github/workflows/fork-release.yml`.
- `internal/updater/updater.go`: `const Repo = "Evgenrzz/rospanel-Amn"` — апдейтер смотрит только в форк.
- `install.sh`: `REPO=`/`IMAGE=` указывают на форк (свежие установки сразу с форка).
- `.release-please-manifest.json` + `internal/version/version.go` — нумерация ведётся вручную.

## 2. Decoy-шаблон tonari-no-totoro (нода 5, tonari-no-totoro.ru)
- `internal/decoy/templates/tonari-no-totoro/**` (41 файл: index.html, страницы, templates/one/*, thumbs) — добавочный каталог, конфликтов с upstream не даёт.
- `web/src/GeneralSettings.tsx` — case `tonari-no-totoro` в выборе маскерада + i18n-ключ totoro.
- Контроль после merge и после сборки релиза: шаблон должен быть ВНУТРИ бинарника (релиз 5.10.0 однажды вышел без него — 46 MB вместо ~62 MB, decoy падал с «missing index.html»). Проверка: `grep -c tonari-no-totoro rospanel-linux-amd64` > 0 и журнал ноды 5 без `decoy template ... missing`.

## 3. Платежи: YooMoney новая подпись уведомлений (v5.10.2)
- `internal/payments/yoomoney.go`: `Webhook()` принимает ОБА формата — новый `sign` (HMAC-SHA256 hex по всем параметрам кроме `sign`, сортировка по алфавиту, значения URL-encoded RFC 3986, пустые как `key=`; секрет из настроек провайдера) и легаси `sha1_hash` (SHA-1 по фиксированному списку полей). Причина: с 18.05.2026 ЮMoney не шлёт `sha1_hash`, upstream-код отбивал все уведомления («bad signature», заказы висели «ожидает»).
- Хелперы `yooMoneySignString()`, `rfc3986()` там же.
- Тест-вектор из официальной доки: `internal/payments/yoomoney_sign_test.go` (+ tamper-тест).
- Кандидат на upstream-PR.

## 4. Платежи: цепочка return-url («Вернуться на сайт») (v5.10.3)
Проблема: хардкод-фолбэк `https://t.me/` в менеджере и литералы в боте уводили плательщика на главную t.me.
- `internal/payments/registry.go`: `ReturnURLKey = "return_url"`.
- `internal/payments/yoomoney.go`, `yookassa.go`: поле карточки провайдера «Куда возвращаться после оплаты» (`return_url`, optional, text) + i18n `payField.returnUrl` / `payHelp.returnUrl` в `web/src/i18n/{ru,en,ar,fa,zh}.ts`.
- `internal/core/manager_payments.go`: цепочка подстановки в `startProviderOrder`: returnURL вызывающего → `providerReturnURL()` (поле карточки, только http(s) с хостом) → `userReturnURL()` (MiniApp пользователя через `sub.MiniAppURL`, иначе его страница подписки) → `https://t.me/` (недостижимый остаток).
- `internal/server/subscription.go`: leaf'ы `pay` и `topup` принимают `return_url` от страницы (валидация `sanitizeReturnURL`: только http(s) с хостом), иначе `sub.URL(set, token)`.
- `internal/sub/view.go`: поле view `miniapp_url` (вход в MiniApp) — страница шлёт его как return_url при оплате из Telegram.
- `internal/telegram/user_purchase.go`, `user_wallet.go`: литералы `"https://t.me/"` заменены на `""` (решает цепочка менеджера).
- ВНЕ ЭТОГО РЕПО: страница подписки (статика subproxy, `subpage/index-redesign.html` на мастере `/opt/subproxy/static/`) шлёт `return_url` из `retURL()`: в MiniApp — `miniapp_url`, в браузере — `location.href`.

## 5. Bounce-страница AppRedirect: страховка TG-webview (ожидает релиза)
- `internal/sub/page.go`: `appRedirectTmpl` детектит TG-webview (UA `Telegram`, `TelegramWebviewProxy`, `Telegram.WebApp`) и вместо слепого `location.replace(scheme://)` показывает большую видимую кнопку-ссылку; авто-редирект оставлен только обычным браузерам. Причина: webview Telegram на `replace(custom-scheme)` остаётся на мёртвой белой странице (инцидент 26.10.2026 со страницей подписки).

## 6. Алерты и аудит-журнал (фичи форка, пришли из fork/main до merge 25.10.2026)
- `internal/core/manager_alerts.go` (+тест), `internal/model/alert.go`, `internal/store/alerts.go`, миграция `0138_alert_rules.sql` (+ `testdata/migrations.sha256`), `internal/server/panel_alerts.go`, `api_v1.go`/`api_v1_perms.go`/`openapi.go`/`audit.go`/`panel.go`, `web/src/AlertsPanel.tsx`, `web/src/api.ts`, i18n-блоки алертов во всех локалях, `internal/plugin/builder/events.go`, `manager_nodes_notify.go`, `manager_events.go`.
- При merge: миграции и `migrations.sha256` сверять особо (upstream добавляет свои номера).

## 7. Вне репозитория (не терять при обновлениях)
- Страница подписки: `c:\github\1\Aweb\subpage\index-redesign.html` → мастер `/opt/subproxy/static/index.html` (subproxy :8444). Кастом: MiniApp-обработка ссылок (webview-навигация для tg:// и VPN-схем, `target=_blank` для импортов/скачиваний), self-hosted шрифты Onest/Unbounded (внешние CDN запрещены: render-blocking убивает страницу при blackhole), `scrollbar-gutter:stable`, `return_url`.
- subproxy: `c:\github\1\Aweb\subproxy\main.go` — карта статики (logo.png, bg-dragon.png, apple-touch-icon.png, fonts/*).
- Парки/деплои: бэкапы живых страниц `_work/live-subpage-prev-*.html`; бэкапы бинарей на мастере `/root/backups/`, `/opt/subproxy/subproxy.bak-*`.

## 8. Чек-лист merge новой версии upstream
1. `git fetch upstream --tags`; изучить diff upstream-тега против файлов разделов 1–6 (`git diff v5.10.0..vX.Y.Z -- <пути>`).
2. Закоммитить всё незакоммиченное локально (чистое состояние перед merge).
3. `git merge vX.Y.Z`; конфликты: `version.go` решаем в пользу НАШЕГО большего номера; код-фиксы, уже принятые upstream, удаляем как дубликаты.
4. Проверить наличие: `.github/workflows/fork-release.yml`, `internal/decoy/templates/tonari-no-totoro/index.html`, `const Repo = "Evgenrzz/rospanel-Amn"`, REPO в `install.sh`.
5. `gofmt`/`go vet`/`go test ./internal/payments/ ./internal/sub/ ./internal/core/` (на Windows тесты `internal/telegram`/`sysstat` не собираются из-за linux-only syscall — это норма, гейт = кросс-сборка).
6. Кросс-сборка `GOOS=linux GOARCH=amd64 go build ./...`; grep бинарника: `tonari-no-totoro`, `Evgenrzz/rospanel-Amn`.
7. Bump версии (выше fleet), коммит, push `main`, тег `v*`, push тега; дождаться релиза; сверить sha256 ассета с `SHA256SUMS` и grep ассета на tonari.
8. Деплой: мастер первым (swap + restart + проверка версии/журнала), затем ноды; для нод без SSH — разовый временный `auto_update_cron` + `auto_update_nodes=1`, после конвергенции вернуть steady-state.
9. Обновить этот файл: новая точка среза diff и новые разделы. Плюс `AGENTS.md`: сверка реестра путей (§2) построчно и запись в журнал работ (§6).

## 9. Порт на upstream v5.14.0 (05.11.2026, ветка `fork-5.14.0`)
- Прямой `merge v5.14.0` в `tonari-decoy` дал ~110 конфликтующих файлов → **отклонён**. Вместо него: свежая ветка от тега `v5.14.0` + тонкий перенос наших дельт (разделы 1–5) поверх апстрима.
- Перенесено целиком (апстрим эти файлы не трогал с v5.10.0): `internal/updater/updater.go`, `install.sh`, `internal/payments/{yoomoney,yookassa,registry}.go` + `yoomoney_sign_test.go`, `internal/telegram/{user_purchase,user_wallet}.go`, `web/src/GeneralSettings.tsx`.
- Перенесено 3-way поверх изменений апстрима: `internal/server/subscription.go`, `internal/core/manager_payments.go`, `internal/sub/page.go` (чисто); `internal/sub/view.go` (конфликт решён: наш `MiniAppURL` + апстрим-поле `WG`).
- i18n (5 локалей): добавлены ТОЛЬКО `maskerade.totoro`, `payField.returnUrl`, `payHelp.returnUrl`. Ключи алертов не переносились.
- Алерты (раздел 6) **не переносились**: приняты upstream (тот же `internal/core/manager_alerts.go` и миграция `0138_alert_rules.sql`); миграции `0139/0140` пришли из апстрима.
- `internal/version/version.go` оставлен `5.14.0` (решение владельца: номер равен апстриму и выше fleet 5.10.3, чтобы после конвергенции ноды не видели расхождения с мастером).
- Гейты пройдены: кросс-сборка `GOOS=linux GOARCH=amd64` ok, бинарник 62.6 MB с вшитыми `tonari-no-totoro` и `Evgenrzz/rospanel-Amn`; `go test ./internal/payments/ ./internal/sub/` ok (`./internal/core/` на Windows не собирается из-за linux-only `sysstat` — норма, гейт = кросс-сборка); web `tsc --noEmit` ok.
- Релиз (тег v5.14.0 в форке) и деплой мастера/нод — отдельным подтверждением владельца.

## 10. Реализм прикрытия нод (manual32 «Защита от ТСПУ», блок 3) — 05.11.2026
- `internal/decoy/decoy.go`: синтез `robots.txt` с вариативностью по движкам (`robotsEngines`: WordPress / DLE / Joomla / Drupal / 1C-Bitrix / plain) — вариант выбирается из per-install Stamp (`u64("robots-engine")`), стабилен на ноде и различен между нодами; `Host:` (и `Sitemap:` у части) от домена запроса; заголовок `Strict-Transport-Security: max-age=31536000` на всех ответах decoy.
- `internal/http80/http80.go`: редирект с :80 теперь **301** (`StatusMovedPermanently`) вместо 308; тест `http80_test.go` обновлён. Обоснование: полевой проб считает 308 признаком, а явно сконфигурированный Caddy обычно отдаёт 301.
- TLS 1.2: не код, а настройка `tls_min13` (generate.go: minTLS=1.2 по умолчанию, 1.3 только при tls_min13). На ноде 13 выставлено `tls_min13=false` → рукопожатие TLSv1.2 проходит.
- `certbot.timer` из блока 3 — **ложный признак для RosPanel**: продление сертификата идёт своим lego/ACME через общий :80 (`http80` + `tlsmgr.ServeHTTP01`), certbot не ставим (конфликт за :80).
- Деплой: нода 13 (animcloud.ru, 179.253.86.104) получила сборку с этими правками ручным swap'ом (sha 43b903d9…), версия оставлена 5.14.0. Для всего fleet правка поедет только с новым релизом (bump версии) — отдельно.
- Известный пре-existing провал теста: `go test ./internal/decoy/` ругается, что `tonari-no-totoro/index.html` грузит внешний https:// (тест запрещает внешние загрузки). К правкам realism не относится; релиз-пайплайн на decoy-тест не гейтится. Чинить отдельно (self-host внешних ресурсов шаблона).
