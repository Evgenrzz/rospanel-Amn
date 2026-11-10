# FORK-CUSTOM — реестр кастомных изменений форка

Этот файл — список всего, чем форк `Evgenrzz/rospanel-Amn` отличается от upstream
`AppsGanin/rospanel`, и чек-лист переноса при merge новой версии upstream.
Актуальный срез диффа: `git diff --stat v5.15.1..HEAD` (точка последнего релиза; ветка разработки `fork-5.15.0`, релиз в `main`).
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

## 7. Страница подписки и subproxy (с 08.11.2026 — ВНУТРИ репозитория)
- Страница подписки: `subpage/index-redesign.html` → мастер `/opt/subproxy/static/index.html` (subproxy :8444). Кастом: MiniApp-обработка ссылок (webview-навигация для tg:// и VPN-схем, `target=_blank` для импортов/скачиваний), self-hosted шрифты Onest/Unbounded (внешние CDN запрещены: render-blocking убивает страницу при blackhole), `scrollbar-gutter:stable`, `return_url`, приветствие и ID (§7.1).
- `subproxy/` — ОТДЕЛЬНЫЙ вложенный Go-модуль (`module subproxy`), НЕ часть модуля панели: `go list ./...` из корня его не видит, поэтому `go build/vet/test ./...` и golangci-lint в CI его не трогают. Плата за это — его тесты в CI тоже не запускаются; гейт локальный: `cd subproxy && gofmt -l . && go vet ./... && go test ./...` (его же прогоняет `_sp_uid.ps1 -Build`). Бинари не коммитятся (`subproxy/.gitignore`).
- Деплой: `_sp_uid.ps1 -All` (гейты → linux-сборка → pscp страницы и бинаря → `_sp_uid.sh` на мастере: бэкап → статика → swap бинаря → restart → self-test). Пароль — из `$env:TEMP\rp_uid_pw.txt`, в репозитории не хранится. Локальный preview: `_work/subpage-preview/`. Бэкапы живых страниц: `_work/live-subpage-prev-*.html`, `_work/live-check.html`; бэкапы на мастере: `/root/backups/`.
- При merge upstream конфликтов не даёт: каталогов `subpage/` и `subproxy/` у апстрима нет.

### 7.1 Приветствие и ID пользователя на странице подписки (08.11.2026)
Проблема: дефолтная страница панели приветствует пользователя по имени (`internal/sub/page.html`: `{{if .Name}}{{printf .L.GreetingName .Name}}{{else}}{{.L.Greeting}}{{end}} 👋`), а кастомная страница не выводила ни имени, ни ID — `renderStatus()` строил только бейджи, плашки и полосу трафика.
- Имя в API уже было: `sub.View.Name` (`internal/sub/view.go`) → `data.name`. Правка чисто фронтовая.
- ID в `sub.View` НЕТ (28 полей, ни одного id) и дефолтная страница его тоже не показывает. Единственная утечка `u<id>` — внутрь ссылок naive/mieru (`naive+https://u5:…@host`), и только при `show_configs`; парсить это нельзя. Поэтому ID добавляет subproxy, который и так резолвит его первым шагом `/api/view`.
- `subproxy/main.go`: `withIdentity(body, id)` в `handleView` — декодирует ответ панели (`json.Decoder` + `UseNumber()`, иначе таймстемпы уезжают в экспоненциальную форму), дописывает `data.user_id`, всё непонятное отдаёт как было. Тесты: `subproxy/main_test.go` (сохранность view + passthrough).
- `subpage/index-redesign.html`: `.hero-row` (flex, baseline, wrap) в `<section class="status">` — `<h1 id="hero">` «Привет, <имя> 👋» и СРАЗУ ЗА НИМ `#uid` с «ID», числом в моноширинной плашке и иконкой копирования (`iconCopy` + класс `uidcp`). Пустое имя → просто «Привет 👋» (то же правило, что у панели). Ключи i18n `greeting`/`greeting_name`/`uid` в RU и `I18N.en`.
- Задеплоено на мастер 08.11.2026: страница sha256 `f6bfbf7f…42077` (94581 b). Проверено: `/api/view` → `name='Евген'`, `user_id=5`, `expire_at` без экспоненты; rospanel active.

### 7.2 Маршрут `/old` убран — редизайн единственный (08.11.2026)
Решение владельца: предыдущая кастомная страница не нужна, окончательная — текущий редизайн.
- `subproxy/main.go`: из карты статики `handleStatic` удалена запись `"/old": "index-old.html"`; маршрут и прямой путь `/index-old.html` отдают 404.
- `/opt/subproxy/static/index-old.html` удалён с сервера. Архив: `/root/backups/subpage-uid-20261108-164501/static/index-old.html` (67801 b, от 14.10.2026) и `_work/live-subpage-prev-*.html`.
- `_sp_uid.sh`: убрана строка, которая воссоздавала `index-old.html` из текущего `index.html` (иначе файл возвращался бы при каждом деплое); добавлен `rm -f` и проверка, что `/old` и `/index-old.html` отвечают 404.

### 7.3 Исходники перенесены в репозиторий (08.11.2026)
Причина: `subpage/` и `subproxy/` не были под git и жили только копией на машине — при переезде workspace папка `subpage/` уже один раз исчезла бесследно, восстанавливать пришлось из `_work/live-check.html`.
- `c:\.U\Aweb\subproxy\{main.go,main_test.go,go.mod}` → `subproxy/`, `c:\.U\Aweb\subpage\index-redesign.html` → `subpage/`. Рабочие копии вне репозитория УДАЛЕНЫ (сверялись по sha256 до удаления: всё идентично, кроме добавленного в `main.go` doc-комментария), чтобы не получить две расходящиеся версии. Удалены и лежалые бинари (`subproxy.exe`, `subproxy_linux_amd64`).
- `_sp_uid.ps1` — новый драйвер деплоя под репозиторные пути; заменяет `_sp_v2.ps1`/`_sp_bin.ps1` (оба указывали на мёртвые `c:\github\1\Aweb\…`).
- Бинарь на мастере пересобран из репозиторного исходника: sha256 `9511543b…77cb`, деплой 16:58 UTC, бэкап `/root/backups/subpage-uid-20261108-165818/`. Self-test: `/` → 200 (94581 b, hero/uid), `/old` и `/index-old.html` → 404, `/etc/passwd` → 404, `/api/view` → `user_id` на месте, rospanel active.

## 8. Чек-лист merge новой версии upstream
1. `git fetch upstream --tags`; изучить diff upstream-тега против файлов разделов 1–7 (`git diff v5.10.0..vX.Y.Z -- <пути>`). Каталоги `subpage/` и `subproxy/` (§7) апстрим не трогает — конфликтов не будет.
2. Закоммитить всё незакоммиченное локально (чистое состояние перед merge).
3. `git merge vX.Y.Z`; конфликты: `version.go` решаем в пользу НАШЕГО большего номера; код-фиксы, уже принятые upstream, удаляем как дубликаты.
4. Проверить наличие: `.github/workflows/fork-release.yml`, `internal/decoy/templates/tonari-no-totoro/index.html`, `const Repo = "Evgenrzz/rospanel-Amn"`, REPO в `install.sh`.
5. `gofmt`/`go vet`/`go test ./internal/payments/ ./internal/sub/ ./internal/core/` (на Windows тесты `internal/telegram`/`sysstat` не собираются из-за linux-only syscall — это норма, гейт = кросс-сборка).
   - **Windows-шум, не считать регрессией (проверено 10.11.2026):** при `core.autocrlf=true` `gofmt -l .` помечает ВСЕ файлы (перевод строк), а `TestMigrationsAreAppendOnly` в `internal/store` валится на всех миграциях сразу — golden `testdata/migrations.sha256` в рабочем дереве с CRLF, `.sql` файлы с LF, поэтому хеши всех строк «не совпали». Настоящий гейт = `GOOS=linux go vet ./...` (0) + CI `ci.yml` на Linux.
   - **Бинарник собирается из `./cmd/rospanel/`, а НЕ из `.`:** `go build -o X .` молча пишет архив библиотеки верхнего пакета (114 КБ, `!<arch>`), а не исполняемый файл — размер ~47 МБ признак правильного targets. Флаги пайплайна: `-trimpath -ldflags "-s -w"`, `CGO_ENABLED=0`, `GOOS=linux`.
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
- `internal/decoy/decoy.go`: синтез `robots.txt` с вариативностью по движкам (`robotsEngines`: WordPress / DLE / Joomla / Drupal / 1C-Bitrix / plain) — вариант выбирается из per-install Stamp (`u64("robots-engine")`), стабилен на ноде и различен между нодами; `Host:` (и `Sitemap:` у части) от домена запроса; `/favicon.ico` алиасится на иконку шаблона, а у шаблонов без иконок (nextcloud) синтезируется минимальный 1x1 ICO (`minimalICO`); заголовок `Strict-Transport-Security: max-age=31536000` на всех ответах decoy.
- `internal/http80/http80.go`: редирект с :80 теперь **301** (`StatusMovedPermanently`) вместо 308; тест `http80_test.go` обновлён. Обоснование: полевой проб считает 308 признаком, а явно сконфигурированный Caddy обычно отдаёт 301.
- TLS 1.2: не код, а настройка `tls_min13` (generate.go: minTLS=1.2 по умолчанию, 1.3 только при tls_min13). На ноде 13 выставлено `tls_min13=false` → рукопожатие TLSv1.2 проходит.
- `certbot.timer` из блока 3 — **ложный признак для RosPanel**: продление сертификата идёт своим lego/ACME через общий :80 (`http80` + `tlsmgr.ServeHTTP01`), certbot не ставим (конфликт за :80).
- Деплой: нода 13 (animcloud.ru, 179.253.86.104) получила сборку с этими правками ручным swap'ом (sha 43b903d9…), версия оставлена 5.14.0. Для всего fleet правка поедет только с новым релизом (bump версии) — отдельно.
- ~~Известный пре-existing провал теста: `tonari-no-totoro/index.html` грузил внешний https://~~ — ИСПРАВЛЕНО 05.11.2026, см. §11 (шаблон обезврежен, `TestDecoysLoadNothingExternal` зелёный).

## 11. Полная персона CMS decoy-слоя (вместо «вариантов robots.txt») — 05.11.2026
Предыстория: §10 делал лишь синтез robots.txt со «случайным движком» — мёртвые обещания (каждый заявленный путь 404) и несогласованность движка с вёрсткой. Заменено цельным слоем «персоны», привязанной к шаблону.
- `internal/decoy/persona.go` (~1200 строк) — персона по slug шаблона (`personaFor`): `10gag`→WordPress, `YouTube`→Joomla, `speedtest`→Drupal, `downloader`→1C-Bitrix, `converter` и `tonari-no-totoro`→DLE, `nextcloud`→Nextcloud, остальные (включая `filecloud`)→static. Версии/даты/токены — из per-install Stamp (`verPools`, `u64("engine-ver")`): стабильны на ноде, различны между нодами.
- Живой эндпоинт-поведенческий слой, сверенный с реальным ПО: WP `/wp-admin`(301)/`/wp-admin/`(302 на login)/`wp-login.php`(+lostpassword/register)/`xmlrpc.php`(GET 405, POST listMethods)/`admin-ajax.php`(400 `0`/403 `-1`)/`feed`,`comments/feed`,`wp-json`,`readme.html`,`wp-cron`,`wlwmanifest`,`blank.gif`, `<meta name=generator>`+RSD+api.w.org; Joomla `/administrator`(+301/200-логин), `cache|components|modules|plugins` 403; Drupal `X-Generator`, `/user`→302, `/core/` 403, `/search/node`, `/rss.xml`; Bitrix `/bitrix/`,`/upload/` 403, `/auth/`,`/personal/`; DLE `/admin.php`,`/login.php`,`/engine/` 403, `?do=rss|opensearch`, `statistics.html`; Nextcloud `status.php`,`/login` 303,`remote.php` 401 WWW-Authenticate,`.well-known/*`; sitemap.xml только с живыми URL (у Nextcloud его нет — как в реальном NC без плагина); persona-404 у CMS (не SPA-фолбэк), `Miss()` согласован; все ответы с Content-Length (ничего не чанкается).
- **Ловушка Go:** `path.Clean` срезает завершающий `/` → `/wp-admin` и `/wp-admin/` — одно имя; без ветки по `strings.HasSuffix(r.URL.Path, "/")` получался бесконечный 301-цикл. То же в Joomla. Проверять при любом новом редирект-эндпоинте.
- **Согласованность:** каждый путь, названный robots/head-ссылками/разметкой логина/manifest, обязан отвечать — закрыто мёртвых обещания (DLE `statistics.html`, `/login.php` у формы, `/search/node`, `index.php/*` NC, trailing-slash `wp-json/`); `robots.txt` never promises dead paths.
- `internal/decoy/decoy.go` — `personaServe` до method-gate(file-server 405), `extraHeaders` на статик-ответы, `robotsAsset`, `isCMS` в `serveMiss`/`Miss`. `filecloud` намеренно static: SPA try_files-фолбэк закреплён существующими тестами.
- **Обезвреживание `tonari-no-totoro`:** 8 идентичных HTML — удалены внешние загрузки (stravers.live iframe → `/templates/one/player.html`, protocol-relative `//kodikplayer.com` → `player2.html`, videoroll-реклама, счётчик counter.yadro.ru), `app.js` poster → локальный; добавлены `templates/one/player.html`,`player2.html` — автономные русские заглушки-плееры без JS/сети. Важно: штатный тест НЕ видит protocol-relative `src="//…"` — его ловит только декой-чек (см. ниже).
- `README*.md` (5 локалей): счётчик шаблонов 12→13 (дрейф с момента добавления tonari, тест `TestREADMEsMatchDecoyTemplateCount` был красный на HEAD).
- `internal/decoy/persona_test.go` — 10 тестов: привязка персоны, robots⇒живые пути, обход всех ссылок персона-страниц (в т.ч. анти-protocol-relative), сигнатуры движков, CMS-404, Content-Length без чанкинга, стабильность на рестарте, разнообразие версий между инсталлами, live-`<loc>` sitemap, POST-поведение.
- `tools/decoycheck/` — локальный харнесс «как дешёвый сканер»: матрица 8 шаблонов (271 проверка, 0 провалов) + `-serve <tmpl> -addr host:port` — живой self-signed HTTPS для ручного curl.
- Гейты 05.11.2026: `go vet` 0; `go test ./internal/decoy/ .` ok; `GOOS=linux go build ./...` 0; decoycheck 271/0.

### Релиз и деплой v5.14.1 — 06.11.2026, по команде владельца («обновляй мастер и ноды»)
Коммиты (ветка `fork-5.14.0`, затем ff-merge в `main`; все запушены в `fork`):
- `81c4a73` — обезвреживание `tonari-no-totoro` (внешние iframe/реклама/счётчик → локальные player.html/player2.html).
- `65b94d8` — `persona.go` + правки `decoy.go` (слой персон, живые эндпоинты, ловушка `path.Clean`).
- `bc536a7` — `persona_test.go` (10 тестов согласованности персон).
- `7ae9967` — `tools/decoycheck` (матрица 271/0 + self-signed `-serve` для ручных curl-проверок).
- `a317af5` — README 12→13 (дрейф счётчика шаблонов).
- `ac8269b` — этот файл + `AGENTS.md` (реестр §2.9, запись журнала).
- `124a3d3` = тег `v5.14.1` (annotated, на `main`): bump `version.go` + `.release-please-manifest.json` 5.14.0→5.14.1.
Релиз: `fork-release.yml` run `42113230435` — зелёный; `rospanel-linux-amd64` 47 870 114 B (у v5.14.0 было 47 767 714 — дельта персоны ~102 КБ, норма), SHA256 `100b5793f6d04d7c32b9d4dbcf1bf1740365fc5079d4ac55bb8350e524900c2e`. Workflow документов `42113196531` упал на `actions/deploy-pages@v4` — GitHub Pages, не гейт кода.
Мастер (78.17.24.97): `_work\deploy_5141_master.sh` через plink — backup бинаря, `sha256sum -c` OK, grep тонари=33/repo/persona OK, swap, restart → `active`, `rospanel 5.14.1`, :80/:443/:8080 слушают.
Ноды (конвергенция временным cron `* * * * *` + `auto_update_nodes=1`, после — возвращено в `''`/`0`): **5/7 на 5.14.1** — 5 ES tonari-no-totoro.ru, 9 IT isburabora.ru, 10 EE kadudu.ru, 12 FI futuramago.ru, 13 BY animcloud.ru; персона проверена живым HTTPS (`_work\persona_live_check.ps1`): DLE `/admin.php` 200, `/no-such-x` 404, player.html 200; Bitrix `/bitrix/` 403 `/auth/` 200, Nextcloud `/status.php` 200 `/login` 303, Drupal `/user` 302 + `X-Generator` на robots.
**Застывшие ноды 2 (LV serial-izvne.ru) и 4 (AL golden-kamuy.ru) — 5.14.0, проблема хостинга, не кода:** их `GET https://api.github.com/repos/Evgenrzz/rospanel-Amn/releases/latest` упорно отдаёт v5.14.0 (в логах нод каждую минуту «node self-update: already on the latest version version=5.14.0»), снаружи тот же endpoint — v5.14.1, draft/prelease нет. Подозрение: локальный «GitHub-ускоритель» (/etc/hosts или прозрачный прокси с кэшем) у этих провайдеров; у ноды 2 дополнительно периодические sync EOF/timeout.
**РЕШЕНО 06.11.2026 ручным swap'ом** (владелец дал SSH-доступ к хостам): `_work\swap_5141_node.sh` через plink — скачивание ассета v5.14.1 напрямую с `github.com/.../releases/download/` (минуем кэшированный `api.github.com`), `sha256sum -c` OK, tonari=33/persona=8, backup бинаря в `/root/backups`, swap, `systemctl restart rospanel-node` → `rospanel 5.14.1`, active — на обеих. В БД мастера 7/7 живых нод на 5.14.1; персона проверена публичным HTTPS: serial-izvne.ru → Joomla (`/administrator/` 200, bare 301, мусор 404), golden-kamuy.ru → Bitrix (`/bitrix/` 403, `/auth/` 200, мусор 404). Хост-футпринты SSH: LV `SHA256:S4CUmTDYaY9dyP5/fi6/LCbBd8f6+G78qldLQh5xBZ0`, AL `SHA256:LYO2aMsP9sKZlcxjIioXKQYIaF6DuL5JgMqSSmy0AeU`. Пароли нод на диске не хранились (pwfile удалены после деплоя).
Диагприём (повторимо): временный full-access API-ключ прямо в SQLite (`key_hash`=HMAC-SHA256(raw, `settings.session_pepper`), `_work\api_dbg_key.sh`), `X-API-Key` + базовый путь `settings.api_path`, `GET /v1/nodes` → `{"data":[…]}`, `GET /v1/nodes/{id}/logs` — хвост лога ноды (первый вызов будит, второй читает). Ключ удалён, временные файлы убраны; в БД остались штатные ключи id1 «sub», id2 «LK».
- Контроль после merge upstream: `internal/decoy/persona.go` — файл форка (upstream его не знает); при конфликте сохраняем наш; прогон `go test ./internal/decoy/` + `go run ./tools/decoycheck` обязателен.

## 12. Порт на upstream v5.15.0 → наш релиз v5.15.1 (10.11.2026, ветка `fork-5.15.0`)
- **Номер релиза сдвинут на патч-цифру: 5.15.1, а не 5.15.0.** Причина: upstream НЕ выпускал 5.14.1 (на remote только `v5.14.0` и `v5.15.0`), наш `v5.14.1` был свободен — а тег `v5.15.0` уже занят объектом upstream в этом репо (`git fetch upstream --tags`), второй тег с тем же именем создать нельзя. 5.15.1 ⇒ строго выше fleet (5.14.1) и не конфликтует с upstream. Внутри себя этот релиз = upstream v5.15.0 + вся дельта форка.
- **Merge прошёл почти чисто: `git merge --no-ff v5.15.0` дал ровно 2 конфликта** (`internal/version/version.go`, `.release-please-manifest.json`), оба — только номер версии. Для сравнения: v5.10.3→v5.14.0 дал ~110 конфликтов и был отклонён (§9). Разница потому, что база сравнения теперь v5.14.0 — наши дельты уже в истории, и `internal/decoy/**` upstream не трогал вообще.
- Пересечение нашей дельты с upstream-изменениями — 16 файлов из 83; автослияние объединило все, кроме двух номерных. Проверено grep'ом после merge (§8 шаг 4 + §2/§11): `tgwebview` в `internal/sub/page.go` (2), `sanitizeReturnURL` в `internal/server/subscription.go` (4), цепочка return-url в `manager_payments.go` (6), `yooMoneySignString` (3) + `yoomoney_sign_test.go`, `ReturnURLKey` (2), `StatusMovedPermanently` в `http80.go`, `tonari-no-totoro` в `GeneralSettings.tsx`, `maskerade.totoro` и `returnUrl` во всех 5 локалях i18n, `const Repo = "Evgenrzz/rospanel-Amn"`, REPO/IMAGE в `install.sh`, `fork-release.yml`, `persona.go` 1253 строки, `persona_test.go`, `tools/decoycheck`, `subproxy/`, `subpage/`, «13 templates» в README.
- **Что принёс upstream (для владельца флота):** лейн **TrustTunnel** (`internal/naive/tt.go`, inbound `trusttunnel-in`, миграция `0142`) — **включён по умолчанию** на панели и на нодах (`settings.trusttunnel_enabled=1`, `nodes.trusttunnel_enabled=1`; `tt://` только на странице подписки, требует домена и доверенного сертификата); пин **Xray-core v26.11.9** (был v26.10.21) — в 26.10.31 выключен MPTCP по умолчанию (remote root), в 26.11.9 uTLS-отпечаток Chrome по умолчанию = 155, ключи генерируемого конфига не изменились; настраиваемые кнопки «открыть в приложении» + свой клиент + мастер по платформам (миграция `0141`: `sub_apps`, `sub_custom_apps`, `sub_app_links`, `sub_wizard_apps`); тёмная тема брендинга (в том же `panel_theme` под ключом `dark`, без миграции); error boundary в SPA; gzip ассетов SPA (1,75 МБ JS → ~498 КБ на проводе).
- **Почему TrustTunnel не ломает персону (§11):** оба лейна ставятся ОБЕРТКОЙ ПЕРЕД decoy — `core.Manager.NaiveHandler = trusttunnel.Wrap(naive.Wrap(next))`, на ноде то же в `nodeagent.naiveHandler`. Туннелем считается только HTTP/2 CONNECT с валидным Basic; всё остальное уходит в decoy ровно как раньше, поэтому `personaServe`/`Miss()` ничего не перехватывают и не отдают вместо tt. После merge: `go test ./internal/naive/ ./internal/link/` ok, `decoycheck` 271/0.
- Миграции `0141`/`0142` — своих у форка нет, пересечения номеров нет; golden `testdata/migrations.sha256` пришёл от upstream целиком.
- Гейты 10.11.2026: `GOOS=linux go vet ./...` = 0; `go test ./internal/decoy/ ./internal/sub/ ./internal/payments/ ./internal/naive/ ./internal/link/ .` ok; `decoycheck` 271 проверок / 0 провалов; `web: npm run build` ok (index 1 754 КБ → gzip 497 КБ); `CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" ./cmd/rospanel/` = 0, бинарник 47 366 304 Б, в нём `tonari-no-totoro`×59, `Evgenrzz/rospanel-Amn`×2, `trusttunnel-in`×2, `TrustTunnel`×26, `wp-login.php`×8, `ErrorBoundary`×19, версия 5.15.1. Локальные провалы `internal/store` и `internal/xray` — артефакты Windows (см. §8 шаг 5), authoritative = CI.
- **Что НЕ переносится автоматически на нашу кастомную страницу подписки:** `subpage/index-redesign.html` (её раздаёт `subproxy`, upstream её не знает) не получает ни настраиваемые кнопки/мастер, ни тёмную тему, ни кнопку `tt://`. Отдельная задача по команде владельца.
