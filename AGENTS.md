# AGENTS.md — правила работы агента в форке rospanel

Файл для агента (Qoder / Claude Code / любой другой). Читается в начале каждой сессии
до любых правок. Дополняет `FORK-CUSTOM.md`: там — реестр диффов, здесь — правила,
порядок действий и журнал работ.

---

## 0. ГЛАВНЫЙ ЗАКОН: всё записывать

**Любое изменение в этом репозитории = три записи, все три обязательны:**

1. **Сами правки** в коде/файлах.
2. **Запись в `FORK-CUSTOM.md`** — раздел с точными путями файлов, причиной изменения
   и номером версии. Если изменение должно пережить обновление upstream — пометка
   «ПЕРЕНОСИТЬ ПРИ MERGE» + путь добавляется в чек-лист раздела 8.
3. **Запись в «Журнал работ» (раздел 6 этого файла)** — дата, что сделано, какие файлы,
   какая версия/деплой, что осталось недоделанным.

Мелких правок без записи НЕ БЫВАЕТ. Правка, которую не записали, теряется при следующем
merge upstream — это уже случалось (force-push снёс `.github/workflows/fork-release.yml`,
релиз 5.10.0 однажды вышел без шаблона tonari-no-totoro внутри бинарника).

Журнал дописывается **в конце каждой сессии**, даже если сессия была только diagnostic
(тогда пишется «изменений в код не вносил, состояние такое-то»).

---

## 1. Что это за репозиторий

Форк **`Evgenrzz/rospanel-Amn`** от upstream **`AppsGanin/rospanel`** (Go-панель управления
VPN-флотом: Xray / AmneziaWG / Mieru / NaiveProxy / AnyTLS / MTProto, React SPA в `web/`,
SQLite в `internal/store`).

| Ветка / ref | Роль |
|---|---|
| `tonari-decoy` (= `fork/main`, запушена) | **БОЕВАЯ линия.** Версия `5.10.3`. Сюда смержен upstream v5.10.0 + все кастомные дельты. Теги `v5.10.1/2/3` выпущены отсюда. |
| `main` (локальная, **не запушена**) | Ветка `v5.0.1-Amanesia` на базе upstream v5.0.1. Несёт нашу caddy-naive реализацию Naive-лейна. **Устарела**: upstream 12.10.2026 выпустил свой `internal/naive`. Не трогать без команды владельца. |
| `upstream/main`, `upstream/*` | Только для чтения. Источник новых версий. |
| тег `pre-naive` | Срез репозитория до работ над naive-лейном (11.10.2026). |

**Форк — ЕДИНСТВЕННЫЙ источник обновлений для мастера и нод.** Upstream напрямую не
ставится никогда. Ноды сами не обновляются: steady-state `auto_update_cron=''`,
`auto_update_nodes=0`.

---

## 2. Реестр кастомных изменений — что ОБЯЗАНО пережить обновление

Полный срез: `git diff --stat v5.10.0..HEAD` (98 файлов, +9204/−308).
Детали и причины — в `FORK-CUSTOM.md`, разделы 1–7. Здесь — сводный список путей для
быстрой сверки после merge.

**2.1 Релиз-пайплайн и указатель на форк** (`FORK-CUSTOM.md` §1)
- `.github/workflows/fork-release.yml` — наш файл, релиз по тегу `v*`. Проверять наличие после любого merge/force-push: `git ls-tree fork/main -- .github/workflows/fork-release.yml`.
- `internal/updater/updater.go` — `const Repo = "Evgenrzz/rospanel-Amn"`.
- `install.sh` — `REPO=` / `IMAGE=` на форк.
- `.release-please-manifest.json`, `internal/version/version.go` — нумерация вручную, СТРОГО выше той, что уже на нодах.
- `.github/workflows/release-please.yml` — выключен тумблером на GitHub. Файл не удалять, воркфлоу не включать.

**2.2 Decoy-шаблон tonari-no-totoro** (§2)
- `internal/decoy/templates/tonari-no-totoro/**` (41 файл) — добавочный каталог.
- `web/src/GeneralSettings.tsx` — case `tonari-no-totoro` + i18n-ключ totoro.
- Контроль: шаблон должен быть ВНУТРИ бинарника — `grep -c tonari-no-totoro rospanel-linux-amd64` > 0.

**2.3 Платежи: YooMoney, новая подпись** (§3, v5.10.2)
- `internal/payments/yoomoney.go` — приём ОБЕИХ подписей: новый `sign` (HMAC-SHA256) и легаси `sha1_hash`. Хелперы `yooMoneySignString()`, `rfc3986()`.
- `internal/payments/yoomoney_sign_test.go` — тест-вектор из официальной доки + tamper-тест.
- Причина: с 18.05.2026 ЮMoney не шлёт `sha1_hash`, upstream-код отбивал все уведомления.

**2.4 Платежи: цепочка return-url** (§4, v5.10.3)
- `internal/payments/registry.go` (`ReturnURLKey`), `yoomoney.go`, `yookassa.go` — поле карточки «Куда возвращаться после оплаты».
- `internal/core/manager_payments.go` — цепочка подстановки в `startProviderOrder`.
- `internal/server/subscription.go` — leaf'ы `pay`/`topup` принимают `return_url` + `sanitizeReturnURL`.
- `internal/sub/view.go` — поле `miniapp_url`.
- `internal/telegram/user_purchase.go`, `user_wallet.go` — литералы `https://t.me/` заменены на `""`.
- `web/src/i18n/{ru,en,ar,fa,zh}.ts` — `payField.returnUrl` / `payHelp.returnUrl`.

**2.5 Bounce-страница AppRedirect** (§5, **ждёт релиза**)
- `internal/sub/page.go` — `appRedirectTmpl`: для TG-webview видимая кнопка вместо слепого `location.replace(scheme://)`.

**2.6 Алерты и аудит-журнал** (§6)
- `internal/core/manager_alerts.go` (+ `_test.go`), `manager_events.go`, `manager_nodes_notify.go`, `manager.go`
- `internal/model/alert.go`, `internal/model/model.go`
- `internal/store/alerts.go`, `internal/store/migrations/0138_alert_rules.sql`, `internal/store/testdata/migrations.sha256`
- `internal/server/panel_alerts.go`, `api_v1.go`, `api_v1_perms.go`, `openapi.go`, `audit.go`, `panel.go`
- `internal/plugin/builder/events.go`, `internal/i18n/{ru,en,ar,fa,zh}.go`, `errcatalog_gen.go`
- `web/src/AlertsPanel.tsx`, `AdminAuditPanel.tsx`, `NodesPanel.tsx`, `TelegramSettings.tsx`, `api.ts`, `web/src/i18n/*.ts`
- **Особое внимание при merge:** номера миграций и `migrations.sha256` — upstream добавляет свои.

**2.7 Документация**
- `README.md`, `README-RU.md`, `README-AR/FA/ZH.md`, `docs/api.md`, `FORK-CUSTOM.md`, `AGENTS.md`.

**2.8 Страница подписки и subproxy** (§7 FORK-CUSTOM; в репозитории с 08.11.2026)
- `subpage/index-redesign.html` → мастер `/opt/subproxy/static/index.html` (subproxy :8444). Кастум: MiniApp-обработка ссылок, self-hosted шрифты Onest/Unbounded (внешние CDN запрещены), `scrollbar-gutter:stable`, `return_url`, приветствие по имени + ID пользователя (§7.1).
- `subproxy/` — ОТДЕЛЬНЫЙ вложенный Go-модуль, НЕ часть модуля панели: `go list ./...` из корня его не видит, CI (`go build/vet/test ./...`, golangci-lint) его не трогает — но и тесты его в CI НЕ бегут. Гейт перед деплоем: `cd subproxy && gofmt -l . && go vet ./... && go test ./...`. `main.go`: карта статики + `withIdentity()` (дописывает `data.user_id` в ответ `/api/view`; в `sub.View` панели id нет). Бинари не коммитятся (`subproxy/.gitignore`).
- Маршрута `/old` больше НЕТ (убран 08.11.2026, §7.2): редизайн — единственная страница, `index-old.html` удалён с сервера. Не возвращать: `_sp_uid.sh` больше не копирует `index.html` в `index-old.html`. Архив — `/root/backups/subpage-uid-20261108-164501/static/`.
- Деплой: `_sp_uid.ps1 -All` (гейты → linux-сборка → upload → `_sp_uid.sh` на мастере). Пароль из `$env:TEMP\rp_uid_pw.txt`, в репозитории не хранится. `_sp_v2.ps1`/`_sp_bin.ps1` — МЁРТВЫЕ (пути `c:\github\1\Aweb\…`), заменены `_sp_uid.ps1`.
- Рабочие копии `c:\.U\Aweb\subpage\` и `c:\.U\Aweb\subproxy\` УДАЛЕНЫ (§7.3) — единственная копия исходников теперь здесь. Не заводить вторую: расхождение версий уже один раз стоило потери `subpage/`.
- Бэкапы живых страниц: `c:\.U\Aweb\_work\live-subpage-prev-*.html`, `_work\live-check.html`; локальный preview `_work\subpage-preview\`; бэкапы на мастере `/root/backups/`.

**2.9 Полная персона CMS decoy** (§11 FORK-CUSTOM, 05.11.2026)
- `internal/decoy/persona.go` — файл форка (upstream его не знает): персона CMS по slug шаблона, живые эндпоинты WP/Joomla/Drupal/Bitrix/DLE/Nextcloud, sitemap/robots-согласованность, persona-404. Персона привязана к шаблону навсегда (`personaFor`) — менять маппинг нельзя без пересогласования robots/sitemap/ссылок.
- `internal/decoy/persona_test.go` — 10 тестов согласия персоны; `internal/decoy/decoy.go` — вызовы `personaServe`/`extraHeaders`/`isCMS`.
- `internal/decoy/templates/tonari-no-totoro/**` — обезврежен от внешних загрузок (player.html/player2.html — локальные заглушки); protocol-relative `src="//…"` штатный тест НЕ ловит — гейт `go run ./tools/decoycheck`.
- `tools/decoycheck/` — верификационный харнесс (матрица 271 проверка + `-serve` для ручного curl).
- Контроль после merge: `go test ./internal/decoy/ .` + `go run ./tools/decoycheck` (0 провалов) + кросс-сборка.

---

## 3. Порядок действий при обновлении (merge новой версии upstream)

1. `git fetch upstream --tags`; изучить diff upstream-тега против файлов раздела 2: `git diff v5.10.0..vX.Y.Z -- <пути>`.
2. Закоммитить всё незакоммиченное локально — перед merge рабочее дерево чистое.
3. `git merge vX.Y.Z`. Конфликты: `version.go` решаем в пользу НАШЕГО большего номера; наши фиксы, уже принятые upstream, удаляем как дубликаты.
4. **Сверка реестра** — пройтись по разделу 2 построчно и проверить наличие каждого пути:
   `git ls-tree -r HEAD --name-only <путь>`. Плюс `.github/workflows/fork-release.yml`,
   `internal/decoy/templates/tonari-no-totoro/index.html`, `const Repo = "Evgenrzz/rospanel-Amn"`, REPO в `install.sh`.
5. `gofmt` / `go vet` / `go test ./internal/payments/ ./internal/sub/ ./internal/core/`.
   На Windows `internal/telegram` и `sysstat` не собираются из-за linux-only syscall — это норма, гейт = кросс-сборка.
6. Кросс-сборка `GOOS=linux GOARCH=amd64 go build ./...`; grep бинарника: `tonari-no-totoro`, `Evgenrzz/rospanel-Amn`.
7. Bump версии (выше fleet) → коммит → push `main` → тег `v*` → push тега → дождаться релиза → сверить sha256 ассета с `SHA256SUMS`.
8. Деплой: **мастер первым** (swap бинаря + restart + проверка версии и журнала), затем ноды. Для нод без SSH — разовый временный `auto_update_cron` + `auto_update_nodes=1`, после конвергенции вернуть steady-state.
9. Обновить `FORK-CUSTOM.md` (новая точка среза diff, новые разделы) и журнал в разделе 6 этого файла.

---

## 4. Правила поведения агента

- **Баг/дефект → сначала предложение.** Формат: «вижу такую-то проблему, могу починить так-то» — и СТОП. Ждать явной команды владельца. Правило действует на все production-поверхности (страница подписки, панель, ноды) и на любые правки кода.
- **Коммиты и push — только по явной команде.** Никогда не коммитить «заодно».
- **Деплой — только по явной команде**, и всегда в порядке мастер → ноды.
- **Языки:** комментарии в коде и сообщения коммитов — английский; `FORK-CUSTOM.md`, `AGENTS.md`, docs-заметки владельца — русский; user-facing строки — все 5 локалей (`ru`, `en`, `ar`, `fa`, `zh`), и в `internal/i18n`, и в `web/src/i18n`.
- **Проектные скилы** (не зарегистрированы в Skill-туле — применять как правила, читая `SKILL.md`):
  - `.agents/skills/go-code-review` — критерий приёмки Go-кода перед коммитом: `bash .agents/skills/go-code-review/scripts/pre-review.sh --force <пакеты>`; находки группировать Must Fix / Should Fix / Nits.
  - `.agents/skills/frontend-design`, `.agents/skills/web-design-guidelines`, `.claude/skills/react-best-practices` — для `web/`.
  - Стиль Go в этом репо: doc-комменты от имени сущности, error strings lowercase, `log/slog` без секретов, `crypto/rand` для кредов, MixedCaps/ID/URL, тесты с got/want.
- **Секреты не логировать** и не записывать ни в один файл репозитория.

---

## 5. Где ещё лежат записи о работах

| Место | Что там |
|---|---|
| `FORK-CUSTOM.md` | Реестр диффов форка + чек-лист merge. **Главный документ.** |
| `AGENTS.md` (этот файл) | Правила + сводный реестр путей + журнал работ. |
| `CHANGELOG.md`, `README*.md`, `docs/api.md` | Только то, что вошло в релиз и видно пользователю. |
| Doc-комментарии пакетов | Основная масса пояснений «почему так» — например шапка `internal/naive/naive.go`. |
| `git log`, `git reflog` | Хронологический журнал: что, когда, в каком порядке. |
| `c:\.U\Aweb\.qoder\plans\*.md` | Планы работ (сейчас: `Naive_lane_for_RosPanel_f0fb243e.md`). |
| Память Qoder | Вне репозитория, в проекте не видна. Правило «сначала предложение» + сводка по форку. |
| `c:\.U\Aweb\_work\`, `_shot\`, `_probe\`, `_up521\`, `_review\` | Черновики, бинарники, скриншоты, зонды. НЕ журнал — только артефакты. |

---

## 6. Журнал работ

Формат записи: **дата — что сделано — файлы — версия/деплой — что осталось.**
Новые записи добавляются СВЕРХУ (свежие первыми) в подраздел «Записи».

### Открытые вопросы
- [ ] **Расхождение двух реализаций naive-лейна.** Наша caddy-naive реализация живёт только на незапушенной `main` (`v5.0.1-Amanesia`); боевая линия использует upstream-овский `internal/naive` (in-process через :443 decoy, креды = Xray email + VLESS UUID). Решение владельца: выбросить нашу версию или перенести из неё отдельное.
- [x] **Bounce-страховка TG-webview** (`internal/sub/page.go`) — выпущена: была уже в v5.14.0 (тэг `5a968af`), и в v5.14.1 (`124a3d3`). Закрывать нечего.
- [ ] **Два незакоммиченных gofmt-диффа** (только выравнивание полей, логики нет): `internal/server/subscription.go`, `internal/sub/view.go`. Закоммитить или откатить — по команде.
- [x] **Маршрут `/old` на subproxy — ЗАКРЫТО 08.11.2026.** Владелец: «/old не нужен, тот который сейчас окончательный». Удалена запись из карты статики `subproxy/main.go`, файл `index-old.html` снят с сервера, `_sp_uid.sh` больше его не воссоздаёт. Архив: `/root/backups/subpage-uid-20261108-164501/static/index-old.html`. Проверено: `/old` и `/index-old.html` → 404, `/` → 200.
- [x] **Ноды 2 (serial-izvne.ru, LV) и 4 (golden-kamuy.ru, AL) — ЗАКРЫТО 06.11.2026.** Стояли на 5.14.0: с их хостов `api.github.com/.../releases/latest` отдаёт v5.14.0 (кэш/ускоритель провайдера). Обновлены вручную (SSH от владельца): ассет v5.14.1 качается напрямую с `github.com/.../releases/download/`, минуя API; swap+restart → 5.14.1, персона проверена публичным HTTPS. Fleet 7/7.

### Записи
- **08.11.2026** — страница подписки: приветствие по имени + ID пользователя (FORK-CUSTOM §7.1). Владелец указал, что кастомная страница (subproxy :8444) не показывает имя, хотя дефолтная страница панели показывает «Привет, Евген 👋». Диагностика: имя уже приходит в `data.name` (`sub.View.Name`) — страница его просто не рисовала; ID в `sub.View` нет вообще, поэтому его добавляет subproxy (`withIdentity()` в `handleView`, `UseNumber()` против экспоненциальной формы таймстемпов) — он и так резолвит id первым шагом `/api/view`. Правки: `subpage/index-redesign.html` (`.hero-row`: `<h1 id="hero">` + `#uid` сразу за именем, i18n `greeting`/`greeting_name`/`uid`), `subproxy/main.go` + новый `subproxy/main_test.go`. Исходник страницы ВОССТАНОВЛЕН в `c:\.U\Aweb\subpage\` — папка пропала при переезде workspace, единственный локальный экземпляр был `_work\live-check.html` (байт в байт с живым). Деплой `_sp_uid.sh` на мастер 78.17.24.97: страница `f6bfbf7f…` 94581 b, бинарь `a4a29c1c…`, бэкап `/root/backups/subpage-uid-20261108-134352/`. Проверено живьём: `/api/view` → `name='Евген'`, `user_id=5`; `/old` не тронут (67801 b от 14.10); **rospanel не перезапускался** (active с 05.11 21:34, sha бинаря та же) — дефолтная страница панели и сам форк не менялись. На тот момент дельта была целиком вне репозитория (позже перенесена внутрь — см. конец записи). Затем тем же днём по команде владельца убран маршрут `/old` (§7.2 FORK-CUSTOM, бинарь `5a913d7a…`, деплой 16:45 UTC): редизайн — единственная страница подписки. И следом исходники перенесены В РЕПОЗИТОРИЙ (§7.3): `subproxy/{main.go,main_test.go,go.mod,.gitignore}` + `subpage/index-redesign.html`, рабочие копии вне репо удалены (сверка по sha256 до удаления), драйвер деплоя `_sp_uid.ps1` вместо мёртвых `_sp_v2.ps1`/`_sp_bin.ps1`. Бинарь на мастере пересобран из репозиторного исходника — sha `9511543b…77cb`, деплой 16:58 UTC, бэкап `/root/backups/subpage-uid-20261108-165818/`; self-test зелёный (`/` 200 + hero/uid, `/old` 404, `/api/view` с `user_id`, rospanel active). Вложенный модуль `subproxy` корневой `go list ./...` не видит — CI панели не задет; обратной стороной его тесты в CI не бегут, гейт ручной (`_sp_uid.ps1 -Build`).
- **06.11.2026 (вечер)** — ручной долив нод 2/4 до 5.14.1 (владелец дал SSH-пароли хостов): `_work\swap_5141_node.sh` через plink — прямой download ассета с `github.com/.../releases/download/v5.14.1/` (обходит кэшированный `api.github.com` у провайдера), sha256 OK, tonari=33/persona=8, backup → swap → restart `rospanel-node` → `rospanel 5.14.1` active на 62.60.234.211 (LV) и 157.228.66.214 (AL). В БД мастера **7/7 живых нод на 5.14.1**, автообновление по-прежнему `''`/`0`. Персона публично: serial-izvne.ru Joomla (`/administrator/` 200, bare 301, 404 на мусор), golden-kamuy.ru Bitrix (`/bitrix/` 403, `/auth/` 200, 404). SSH-футпринты хостов записаны в FORK-CUSTOM §11; временные pwfile'ы удалены.
- **06.11.2026** — релиз и деплой v5.14.1 (персона CMS, FORK-CUSTOM §11 «Релиз и деплой»), по команде владельца. Коммиты `81c4a73`→`ac8269b` в `fork-5.14.0`, ff-merge в `main`, bump 5.14.0→5.14.1 (`124a3d3`, annotated-тэг `v5.14.1`), всё запушено в `fork`. Релиз `fork-release.yml` run `42113230435` зелёный: amd64-ассет 47 870 114 B, SHA256 `100b5793…` (дельта +102 КБ от v5.14.0 — персона). Мастер (78.17.24.97): swap по sha + grep tonari=33/persona, restart → `rospanel 5.14.1`, active. Ноды: временный cron-конвергенция (`* * * * *`+`auto_update_nodes=1` → назад `''`/`0`) — **5/7 на 5.14.1** (5,9,10,12,13), персона проверена живым HTTPS (DLE/Bitrix/Nextcloud/Drupal, `X-Generator` на robots). Ноды 2/4 застыли: с их хостов `releases/latest` отдаёт v5.14.0 (кэш провайдера), см. Открытые вопросы. Диагностика через временный full-access API-ключ в SQLite (HMAC по `session_pepper`) + `/v1/nodes/{id}/logs`; ключ и временные файлы удалены.
- **05.11.2026** — полная персона CMS decoy (FORK-CUSTOM §11): `internal/decoy/persona.go` — движок привязан к шаблону (10gag→WP, YouTube→Joomla, speedtest→Drupal, downloader→Bitrix, converter+tonari→DLE, nextcloud→Nextcloud, прочее→static), живые эндпоинты сверены с реальным ПО (редиректы, коды, заголовки, генераторы, sitemap только с живыми URL, CMS-404 вместо SPA-фолбэка); версия/даты из Stamp — стабильны на ноде, различны между нодами. Убита ловушка `path.Clean` (срезание `/` → бесконечный 301-цикл `/wp-admin/`). Обезврежен `tonari-no-totoro` (внешние iframe/реклама/счётчик → локальные player.html/player2.html). README 12→13 (тест был красный на HEAD). Новое: `persona_test.go` (10 тестов), `tools/decoycheck` (матрица 271/0 + self-signed `-serve`). Гейты: vet 0, `go test ./internal/decoy/ .` ok, linux-сборка 0. Закоммичено и выпущено 06.11 — см. запись выше.
- **05.11.2026** — realism прикрытия (manual32 блок 3): decoy отдаёт синтезированный `robots.txt` + HSTS; :80 редирект 301 вместо 308; нода 13 (animcloud.ru) `tls_min13=false` → TLS1.2 проходит; certbot.timer признан ложным признаком (lego/ACME). Сборка sha 43b903d9… залита на ноду 13 ручным swap'ом (версия 5.14.0). Fleet — только с новым релизом. Зафиксировано в FORK-CUSTOM §10.
- **05.11.2026** — порт боевой линии на upstream v5.14.0: новая ветка `fork-5.14.0` от тега v5.14.0 (прямой merge v5.10.3→v5.14.0 дал ~110 конфликтов и отклонён). Тонким слоем перенесены: YooMoney HMAC (§2.3), цепочка return-url (§2.4), bounce TG-webview (§2.5), tonari decoy + case в GeneralSettings + i18n-ключи (5 локалей), инфра форка (fork-release.yml, updater Repo, install.sh REPO/IMAGE). Алерты (§2.6) НЕ переносились — приняты upstream (та же миграция 0138). version.go = 5.14.0 (решение владельца). Гейты: кросс-сборка linux/amd64 ok (62.6 MB, tonari+Evgenrzz вшиты), go test payments/sub ok (core не собирается на Windows из-за linux-only sysstat — норма), web tsc --noEmit ok. Релиз и деплой — отдельным подтверждением.
- **05.11.2026** — создан этот `AGENTS.md` (правила записи, сводный реестр путей, журнал). Изменений в код не вносил. Состояние: `tonari-decoy` = `fork/main` = `326e90f`, версия 5.10.3, теги v5.10.1/2/3 в форке; два gofmt-диффа не закоммичены; bounce-страховка ждёт релиза.
- **28.10.2026** — v5.10.3: цепочка return-url (§2.4) — `8b6f840`; создан `FORK-CUSTOM.md` + bounce-страховка TG-webview в `internal/sub/page.go` — `326e90f`. Тег `v5.10.3` запушен в форк.
- **27.10.2026** — v5.10.2: YooMoney HMAC-SHA256 `sign` наряду с `sha1_hash` (§2.3) + тест-вектор — `406fc38`. Бинарь `_work/rospanel-v5102-amd64`.
- **26.10.2026** — инцидент: TG-webview на странице подписки остаётся на мёртвой белой странице после `location.replace(scheme://)`. Правки вне репо (`subpage/index-redesign.html`, subproxy) + страховка в панели (выпущена 28.10). Артефакты: `_work/sub-isolated.png`, `sub-fonts-blocked.png`, `sub-redesign-live.png`, `subproxy-linux-amd64-new`.
- **25.10.2026** — merge `fork/main` в `tonari-decoy` (`8fb3c82`): приехали алерты и аудит-журнал (§2.6, `e23d980`, `434709c`). Восстановлен `fork-release.yml`, снесённый force-push'ом (`06d7b21`, тег v5.10.1). Бинарники: `_work/rospanel-v5101-amd64`, `rospanel-5.10.1-tonari`.
- **24.10.2026** — `custom(5.8.3)`: tonari decoy + fork Repo + xray pin v26.10.21 + фикс `config.new.json` (`01fd287`); затем merge upstream v5.10.0 + custom 5.10.1, tonari и fork Repo сохранены (`e9b5907`). Бинарник `rospanel-linux-amd64` в корне workspace.
- **11–12.10.2026** — ветка `main`: self-update, `install.sh` и GHCR-образ переведены на форк (`b49f18b`), re-trigger CI (`1faf35d`), fork tag-release pipeline + бренд версии `5.0.1-Amanesia` (`c4ca134`).
- **11.10.2026** — Naive-лейн по плану `.qoder/plans/Naive_lane_for_RosPanel_f0fb243e.md`: фазы 1–6d, 11 коммитов (`c984cb7` core → `c924e99` docs), файлы `internal/naive/{render,accesslog,ensure,supervisor,tail,client,naive}.go` + тесты, node-side супервизор, выдача в подписке, UI в `ConnectionsEditor.tsx`, i18n, тесты reserved-порта и cert-handoff, фикс `user_id` в access-логе caddy. Срез до работ — тег `pre-naive` и `rospanel-pre-naive-20261011.zip`. **В боевую линию не вошёл**: 12.10.2026 upstream выпустил свой `internal/naive` (v5.1.0), он и работает сейчас.
