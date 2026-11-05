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

**2.8 ВНЕ репозитория — не терять** (§7)
- Страница подписки: `c:\.U\Aweb\subpage\index-redesign.html` → мастер `/opt/subproxy/static/index.html` (subproxy :8444). Кастум: MiniApp-обработка ссылок, self-hosted шрифты Onest/Unbounded (внешние CDN запрещены), `scrollbar-gutter:stable`, `return_url`.
- subproxy: `c:\.U\Aweb\subproxy\main.go` — карта статики.
- Бэкапы живых страниц: `c:\.U\Aweb\_work\live-subpage-prev-*.html`; бэкапы бинарей на мастере: `/root/backups/`, `/opt/subproxy/subproxy.bak-*`.

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
- [ ] **Bounce-страховка TG-webview** (`internal/sub/page.go`) в коде есть, релиза ещё не было — выпустить вместе со следующим тегом.
- [ ] **Два незакоммиченных gofmt-диффа** (только выравнивание полей, логики нет): `internal/server/subscription.go`, `internal/sub/view.go`. Закоммитить или откатить — по команде.

### Записи
- **05.11.2026** — порт боевой линии на upstream v5.14.0: новая ветка `fork-5.14.0` от тега v5.14.0 (прямой merge v5.10.3→v5.14.0 дал ~110 конфликтов и отклонён). Тонким слоем перенесены: YooMoney HMAC (§2.3), цепочка return-url (§2.4), bounce TG-webview (§2.5), tonari decoy + case в GeneralSettings + i18n-ключи (5 локалей), инфра форка (fork-release.yml, updater Repo, install.sh REPO/IMAGE). Алерты (§2.6) НЕ переносились — приняты upstream (та же миграция 0138). version.go = 5.14.0 (решение владельца). Гейты: кросс-сборка linux/amd64 ok (62.6 MB, tonari+Evgenrzz вшиты), go test payments/sub ok (core не собирается на Windows из-за linux-only sysstat — норма), web tsc --noEmit ok. Релиз и деплой — отдельным подтверждением.
- **05.11.2026** — создан этот `AGENTS.md` (правила записи, сводный реестр путей, журнал). Изменений в код не вносил. Состояние: `tonari-decoy` = `fork/main` = `326e90f`, версия 5.10.3, теги v5.10.1/2/3 в форке; два gofmt-диффа не закоммичены; bounce-страховка ждёт релиза.
- **28.10.2026** — v5.10.3: цепочка return-url (§2.4) — `8b6f840`; создан `FORK-CUSTOM.md` + bounce-страховка TG-webview в `internal/sub/page.go` — `326e90f`. Тег `v5.10.3` запушен в форк.
- **27.10.2026** — v5.10.2: YooMoney HMAC-SHA256 `sign` наряду с `sha1_hash` (§2.3) + тест-вектор — `406fc38`. Бинарь `_work/rospanel-v5102-amd64`.
- **26.10.2026** — инцидент: TG-webview на странице подписки остаётся на мёртвой белой странице после `location.replace(scheme://)`. Правки вне репо (`subpage/index-redesign.html`, subproxy) + страховка в панели (выпущена 28.10). Артефакты: `_work/sub-isolated.png`, `sub-fonts-blocked.png`, `sub-redesign-live.png`, `subproxy-linux-amd64-new`.
- **25.10.2026** — merge `fork/main` в `tonari-decoy` (`8fb3c82`): приехали алерты и аудит-журнал (§2.6, `e23d980`, `434709c`). Восстановлен `fork-release.yml`, снесённый force-push'ом (`06d7b21`, тег v5.10.1). Бинарники: `_work/rospanel-v5101-amd64`, `rospanel-5.10.1-tonari`.
- **24.10.2026** — `custom(5.8.3)`: tonari decoy + fork Repo + xray pin v26.10.21 + фикс `config.new.json` (`01fd287`); затем merge upstream v5.10.0 + custom 5.10.1, tonari и fork Repo сохранены (`e9b5907`). Бинарник `rospanel-linux-amd64` в корне workspace.
- **11–12.10.2026** — ветка `main`: self-update, `install.sh` и GHCR-образ переведены на форк (`b49f18b`), re-trigger CI (`1faf35d`), fork tag-release pipeline + бренд версии `5.0.1-Amanesia` (`c4ca134`).
- **11.10.2026** — Naive-лейн по плану `.qoder/plans/Naive_lane_for_RosPanel_f0fb243e.md`: фазы 1–6d, 11 коммитов (`c984cb7` core → `c924e99` docs), файлы `internal/naive/{render,accesslog,ensure,supervisor,tail,client,naive}.go` + тесты, node-side супервизор, выдача в подписке, UI в `ConnectionsEditor.tsx`, i18n, тесты reserved-порта и cert-handoff, фикс `user_id` в access-логе caddy. Срез до работ — тег `pre-naive` и `rospanel-pre-naive-20261011.zip`. **В боевую линию не вошёл**: 12.10.2026 upstream выпустил свой `internal/naive` (v5.1.0), он и работает сейчас.
