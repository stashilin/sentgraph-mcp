# Amp: плагины для sentgraph (разведка 13.08.2026)

Хуков в стиле Claude Code у Amp нет. Есть лучшее: **Plugin API** (с переписывания «Neo»,
май 2026) — TypeScript-модули в Bun-процессе, событийная шина. Архитектурно это близнец
механизма omp: адаптеры для Amp и omp — родственники, обоим нужен общий generic-вход
записи в Go-бинаре.

Проверено на установленном Amp `0.0.1786320433-geeee54` (10.08.2026); полный дамп
Plugin API этой версии снимается командой `amp plugins show-docs`.

## Контракт Plugin API (ampcode.com/manual/plugin-api)

- Плагин — `.ts`-файл: проектный `<repo>/.amp/plugins/`, юзерский `~/.config/amp/plugins/`,
  либо Amp-hosted (`amp clone user-plugins` — все машины, анонс Global Plugins 11.08.2026).
- События `amp.on(...)`:
  - `agent.start` — на каждый промпт; хендлер может вернуть `{message: {content, display?}}` —
    текст дописывается к user message (аналог `additionalContext` в UserPromptSubmit);
  - `agent.end` — конец хода; payload несёт **весь ход**: `messages: ThreadMessage[]`
    (user/assistant/thinking/tool_use/tool_result), `status`, `thread.id`;
  - `session.start`, `tool.call` (allow/reject/modify/synthesize), `tool.result`.
- Контекст: `ctx.$` — **Bun shell** (можно звать sentgraph-mcp), `ctx.system.workspaceRoot`
  (для поиска `.env.local`), `ctx.thread.messages()` (пагинация, ≤20 за вызов),
  `ctx.ui`, `amp.ai.ask`.
- **Тест без токенов**: `amp plugins exec <file.ts> <event> --data '<json>'` — прогон
  хендлера на синтетическом событии, LLM не дёргается. Это же путь приёмки.
- В `amp -x` (execute-режим) нужен `--plugin-ready-timeout`, иначе хендлеры могут
  не успеть подняться.

## Прочие механизмы (для полноты)

- **Toolbox (`AMP_TOOLBOX`) — умер**: встроенный скил building-plugins прямо пишет
  «Toolboxes are no longer supported», замена — `amp.registerTool(...)`.
- MCP: `amp.mcpServers` в `~/.config/amp/settings.json` (9 серверов сейчас, sentgraph нет).
  В `amp.mcpTrustedServers` осталась trust-запись `sentgraph` — когда-то его уже одобряли
  как workspace-MCP (ещё один след полуустановки), сейчас никто не декларирует.
- Скилы: Amp читает и клодовские (`~/.claude/skills/` и т.д.) — 366 штук видит уже сейчас.
- `~/.config/amp/AGENTS.md` — статичное напоминание «пользуйся памятью», читается на старте.
- Треды — **облако ampcode.com**, локально контента нет. Экспорт: `amp threads export <id>`
  (путь для backfill истории). Есть `/api/v2/threads/{id}/messages` (workspace-скоупы,
  доступность на индивидуальном аккаунте не проверена).
- `amp -x --stream-json` — формат, совместимый с Claude Code stream-json.
- Встроенной межтредовой памяти у Amp нет, handoff удалён в Neo. Memory-интеграций
  (mem0/zep/letta) в экосистеме Amp не существует — ниша свободна.

## План адаптера

1. В Go-бинаре — generic-команда `ingest`: JSON-сообщения на stdin, агентно-независимый
   формат (роль, текст, thread_id, cwd), redact и роутинг как в hook. Её же используют
   Codex-адаптер (при желании) и omp-адаптер Стаса.
2. Плагин `~/.config/amp/plugins/sentgraph.ts` (~100 строк):
   - `agent.end` → `ctx.$` → `sentgraph-mcp ingest` (маппинг ThreadMessage[] тривиален);
   - `agent.start` → `memory_context` из бинаря → `{message: {content, display: false}}`;
     впрыскивать раз на тред (трекать thread.id) либо каждый ход — как в Claude.
3. **Обязательно**: впрыснутый в `agent.start` текст сохраняется в тред — при записи хода
   его вырезать, иначе память начнёт жевать саму себя.
4. Приёмка: `amp plugins exec` на синтетических событиях (бесплатно) + одна живая
   сессия → записал → нашёл.

## Риски

- Plugin API молодой (3 месяца), Amp его активно меняет — проверять changelog при
  обновлениях; «Most APIs are stable», экспериментальное — под `amp.experimental`.
- Хендлеры disposal ограничены ~3 с; тяжёлую запись делать неблокирующей.
- Треды и так синкаются в облако Amp — redact обязателен до пересылки в Zep
  (секрет, попавший в тред, уже утёк на ampcode.com независимо от нас).
