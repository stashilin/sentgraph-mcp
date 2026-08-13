# Codex CLI: хуки для sentgraph (разведка 13.08.2026)

Codex CLI ≥0.124 имеет **стабильную систему lifecycle-хуков, намеренно совместимую с Claude Code**
(проверено на установленном 0.145.0: формат hooks.json, события и stdin/stdout-контракт совпадают;
в бинаре даже комментарий «Claude requires `reason` when `decision` is `block`»).
Это значит: `sentgraph-mcp hook <event>` почти plug-compatible, отдельный backfill-хвост не нужен.

## Контракт (сверено с бинарём 0.145.0 и docs: developers.openai.com/codex/hooks)

- Источники хуков: `~/.codex/hooks.json` (user), `<repo>/.codex/hooks.json` (project),
  плагины (`hooks/hooks.json`, `$PLUGIN_ROOT`). Слои складываются, не замещаются.
- События: SessionStart, SessionEnd, UserPromptSubmit, Stop, PreCompact, PostCompact,
  PreToolUse, PostToolUse, PermissionRequest, SubagentStart, SubagentStop.
- Вход — JSON на stdin, поля как у Claude Code (`session_id`, `cwd`, `hook_event_name`,
  `transcript_path`…). Отличия в плюс:
  - `Stop` несёт **`last_assistant_message` прямо в stdin** — транскрипт можно не парсить;
  - `UserPromptSubmit` несёт `prompt`;
  - `SessionStart.source`: `startup|resume|clear|compact` — после компакции хук перевызывается
    (у Claude Code для этого отдельный PreCompact-костыль).
- Выход — JSON на stdout, `hookSpecificOutput.additionalContext` работает в SessionStart,
  UserPromptSubmit, SubagentStart, PostToolUse. Лимит ~2500 токенов, поднимается
  per-handler полем `additionalContextLimit`. `"async": true` — фоновое выполнение.
- Trust: каждый хук пинуется в config.toml (`[hooks.state."<путь>:<событие>:<i>:<j>"]`,
  `trusted_hash = "sha256:..."`). Правка hooks.json → повторное подтверждение через `/hooks`
  (интерактив, руки пользователя) либо managed-путь через `requirements.toml`.

## Отличия, требующие доработки бинаря

1. `transcript_path` указывает на **rollout-формат** (`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`,
   строки `{timestamp, type, payload}`, типы `session_meta|response_item|event_msg|turn_context|…`) —
   `internal/transcript` его не распарсит. Для Stop это не блокер: брать `last_assistant_message`
   из stdin-пейлоада и не трогать транскрипт. Парсер rollout — отдельным шагом, если понадобится
   (формат БЕЗ контракта стабильности, ломался между версиями — issue openai/codex#20952).
2. Валидация путей транскриптов: разрешённые корни дополнить `~/.codex/sessions`
   (только если парсер всё же делаем).
3. `SessionEnd.reason` пока константа `"other"`; SessionEnd всегда синхронный, таймаут 1–3 с.

## Известные грабли (issues openai/codex, актуальны на 13.08.2026)

- **#26383**: `codex exec` не диспатчит project-scope хуки (`<repo>/.codex/hooks.json`) —
  ставить в user-scope `~/.codex/hooks.json`; наш контракт `.env.local`
  («hook молча выходит без файла») делает user-scope безопасным.
- **#34694**: async-хуки Claude-формата из плагинов молча пропускаются — регистрировать хуки
  прямыми записями в hooks.json, не через плагин (пока баг жив).
- **#35306**: project-хуки могут молча скипаться без trust-промпта.
- `~/.codex/hooks.json` на этой машине уже занят (codex-companion, atuin, protect-dirs) —
  дописывать записи, не перезаписывать файл.
- `notify` не годится: одно событие, слот один и занят (Computer Use).

## Конфликт с нативной памятью Codex

С 0.145 у Codex включена собственная память: `[memories] generate_memories = true,
use_memories = true` (`~/.codex/memories/`, извлечение и консолидация из роллаутов моделью).
При подключении sentgraph получится двойная память. Варианты: выключить
(`memories.use_memories = false`), или оставить обе и посмотреть на шум; есть тюнинг
`disable_on_external_context`. Решение за владельцем.

## План адаптера

1. В `internal/hooks`: ветка для Codex-пейлоадов — Stop берёт `last_assistant_message`
   из stdin (вместо чтения транскрипта), UserPromptSubmit — `prompt` (уже так).
   Определение источника — по полям пейлоада, не по эвристикам.
2. Записи в `~/.codex/hooks.json` (append): SessionStart (matcher `startup|resume|clear|compact`),
   UserPromptSubmit, Stop (`"async": true`), SessionEnd. Команды — те же
   `sentgraph-mcp hook <event>`.
3. Однократный интерактивный trust через `/hooks` в Codex — руки владельца.
4. Приёмка: контрольная сессия Codex → записал → нашёл (в т.ч. из Claude-сессии,
   графы общие).

Прецедент такой же интеграции: Hindsight/Vectorize (SessionStart warm + UserPromptSubmit
inject + Stop retain). Zep-интеграций с Codex в природе не найдено — ниша свободна.

Codex 0.146+ заявляет установку плагинов из маркетплейсов **формата Claude Code** — т.е.
`.claude-plugin/marketplace.json` этого репо потенциально ставится в Codex напрямую
(`codex plugin add`). Не проверено; после починки #34694 может заменить ручные записи
в hooks.json.
