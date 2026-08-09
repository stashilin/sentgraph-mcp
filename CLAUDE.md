# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Что это

Go MCP-сервер долговременной памяти для кодинг-агентов на базе Zep Cloud. Один самодостаточный бинарь `sentgraph-mcp` с тремя режимами:

- `serve [--http ADDR]` — MCP-сервер (stdio по умолчанию; `--http` — Streamable HTTP)
- `hook <event>` — обработчик lifecycle-хука Claude Code (JSON из stdin)
- `doctor [--online]` — проверка конфигурации и связи с Zep

Локальный слой намеренно тонкий: построение графа, дедупликация, эмбеддинги и извлечение — на стороне Zep Cloud.

## Команды

```bash
go build ./...                                   # сборка
go test ./...                                    # все тесты
go test ./internal/config                        # тесты одного пакета
go test ./internal/hooks -run TestHandle         # один тест
go vet ./...                                     # статический анализ
go run . doctor --online                         # проверка конфига + связи с Zep (нужен .env.local)
```

Локальная сборка имеет версию `dev`; релизная инжектится через `-ldflags "-X main.version=..."` (GoReleaser).

## Архитектура

Поток данных: Claude Code → (`serve` как MCP по stdio | `hook <event>` из plugin/hooks/hooks.json) → `internal/memory.Service` → `internal/zepstore.Store` → Zep Cloud.

- `internal/config` — конфигурация из env, засеянная из ближайшего `.env.local` (поиск вверх от `CLAUDE_PROJECT_DIR` или cwd; godotenv non-override — реальный env побеждает). Обязательные ключи: `ZEP_API_KEY`, `ZEP_USER_ID`, `SENTGRAPH_PROJECT_ID`. Тюнинг-переменные (`SENTGRAPH_INJECT_EVERY_PROMPT`, `SENTGRAPH_PROJECT_AUTOCAPTURE`, `SENTGRAPH_CAPTURE_TOOLS`, `SENTGRAPH_CONTEXT_TOKEN_BUDGET`) парсятся, но в хуки пока не подключены (TODO в config.go).
- `internal/memory` — бизнес-логика (`Service`): identity, маршрутизация user/project, редакция секретов перед отправкой. Zep скрыт за интерфейсом `Store` — тесты подменяют его фейком.
- `internal/zepstore` — единственный пакет, знающий про SDK `zep-go`; реализует `Store`.
- `internal/mcpserver` — регистрирует шесть MCP-инструментов (`memory_context`, `memory_search`, `memory_history`, `memory_add_messages`, `memory_add`, `memory_forget`) поверх `memory.Service`.
- `internal/hooks` — маппит пять событий Claude Code (SessionStart, UserPromptSubmit, PreCompact, Stop, SessionEnd) на операции памяти: read-хуки печатают additionalContext в stdout, write-хуки сохраняют ходы. Путь транскрипта валидируется против списка разрешённых корней.
- `internal/transcript` — парсит JSONL-транскрипт Claude Code, достаёт последний ход ассистента.
- `internal/redact` — вырезает секреты по regex-паттернам высокой уверенности; применяется до любой отправки в Zep. Ничего секретного в память уходить не должно.

Модель идентичности: два графа в Zep — граф пользователя (`ZEP_USER_ID`) и граф проекта (`graph_id = "proj:" + SENTGRAPH_PROJECT_ID`); один проект может охватывать несколько репозиториев. Треды — по сессиям.

Контракт `.env.local` (защита от глобальной установки): `serve` и `doctor` отказываются стартовать без файла (`RequireEnvFile`); `hook` без файла молча выходит, чтобы user-scope установка не спамила ошибками в чужих проектах. Ошибка загрузки найденного файла (синтаксис/права) поднимается наружу, а не маскируется под «ключ не задан».

## Плагин Claude Code

Репозиторий сам является маркетплейсом плагина: `.claude-plugin/marketplace.json` → плагин `sentgraph` из `./plugin` (hooks.json + пять скилов: remember, recall, forget, session-history, sentgraph-tools). Хуки вызывают глобально установленный бинарь `sentgraph-mcp`; ставится плагин только с `--scope project`. Изменил поведение хуков в Go-коде — проверь согласованность с `plugin/hooks/hooks.json` и README.

## Релиз

Сборку описывает `.goreleaser.yaml` (darwin/linux × amd64/arm64, Homebrew cask в `shilin23061991/homebrew-tap`, нужен секрет `HOMEBREW_TAP_GITHUB_TOKEN`). Внимание: workflow GitHub Actions удалён (коммит 6ed1797), так что пуш тега сейчас ничего не запускает — README в разделе «Релиз» устарел.

## Конвенции

- README, комментарии-докстроки в плагине и сообщения коммитов — на русском; идентификаторы и код — на английском.
- Дизайн-заметки: `docs/implementation-plan.md`, `zep-memory.md`.
