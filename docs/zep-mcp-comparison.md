# Zep официальные варианты vs sentgraph-mcp

Дата анализа: 2026-08-09. Все факты измерены: живые страницы help.getzep.com (суффикс `.md` отдаёт чистый markdown), GitHub API по org getzep, curl-probe MCP-эндпоинтов, чтение локального кода. Первоисточники скачаны в scratchpad сессии (`zepresearch/`).

## Что именно добавил Zep

1. **Memory MCP Server** (hosted, `https://api.getzep.com/mcp`) — remote MCP, дающий конечному пользователю доступ к его памяти Zep из любого MCP-клиента. Включается **per account через sales@getzep.com**, самообслуживания нет. Auth — **только SSO** (OAuth 2.1 + PKCE через Google Workspace, либо Custom OIDC = Enterprise-план). Варианта «просто API-ключ» нет. Токены ~5 мин, авто-renew.
2. **Docs MCP Server** (`https://docs-mcp.getzep.com/mcp`) — поиск по документации, 1 инструмент `search_documentation` + 281 ресурс-страница. Бесплатно, без ключа.
3. **Build with Zep plugin** (`getzep/building-with-zep-plugin`, v0.3.0 от 2026-08-07) — design-time помощник: скил `building-with-zep` + docs-MCP. Runtime-памяти и хуков **нет** («provisions nothing itself»).
4. **Graphiti MCP** — experimental, полностью self-hosted (Docker + FalkorDB/Neo4j + свои LLM-ключи), это НЕ клиент Zep Cloud.
5. Старый `zep-mcp-server` (Go, по API-ключу, 13 read-only инструментов) — **deprecated**: «Use the official Zep Memory MCP server instead». Поддерживаемого MCP по API-ключу у Zep больше нет.
6. Репо `getzep/zep-memory-plugin` (push 2026-08-08, main пустой): готовящийся плагин памяти — по README рабочей ветки **«Not positioned for Claude Code, Codex, Cursor»**, целевые поверхности Claude Desktop Chat / Claude Cowork / ChatGPT Work; **«hooks / auto-ingest of every message are out of scope»** для v1.

## Сравнительная таблица

| Ось | sentgraph-mcp (наш) | Zep Memory MCP Server | Build with Zep plugin | Graphiti MCP |
|---|---|---|---|---|
| Тип | локальный Go-бинарь (stdio/HTTP) | hosted remote MCP (HTTP) | скил + docs-MCP | self-hosted (Docker) |
| Runtime-память | **да** (Zep Cloud) | да (Zep Cloud) | нет, design-time | да, но свой бэкенд (не Zep Cloud) |
| Автозахват беседы | **да**: хуки UserPromptSubmit / Stop / SessionEnd пишут ходы сами | **нет** — pull-модель, агент должен сам звать инструменты | нет | нет |
| Автоинъекция контекста | **да**: SessionStart / PreCompact / UserPromptSubmit → additionalContext | нет | — | нет |
| MCP-инструменты | 6: memory_context, memory_search, memory_history, memory_add_messages, memory_add, memory_forget | 6: search_graph (scope: auto/observations/thread_summaries/episodes), get_user_summary, add_memory, list_graphs, search_graph_in, add_memory_to_graph (+ ресурс `zep://graphs/directory`) | 1: search_documentation | 9 (add_memory, search_nodes, search_memory_facts, …, clear_graph) |
| Богатство поиска | scope edges/nodes/episodes | scope auto (optimized context block), observations, thread_summaries, episodes — **шире нашего** | — | bi-temporal фильтры valid_at/invalid_at |
| Redaction секретов | **8 regex-паттернов до отправки** (JWT, sk-, gh*, AKIA, AIza, xox, Bearer) | не упоминается нигде (поиск по их докам — «No results found») | — | нет |
| Auth | ZEP_API_KEY в `.env.local` проекта | только SSO: Google Workspace или Custom OIDC (Enterprise) | без ключа | свои LLM-ключи (OpenAI и др.) |
| Скоупинг проекта | ключи из env либо `.env.local` в самой директории проекта (вверх не ищем) + граф `proj:<id>`; в ненастроенном проекте hook молча выходит | токен жёстко привязан к проекту, инструменты не принимают user/project — **сильная модель** | — | свой инстанс = свой скоуп |
| Governance | нет (личный инструмент) | admin: read-only per connection, аккаунт-левел writes kill switch, мгновенная revocation | — | нет |
| Доступность | self-service (brew / go install) | **contact sales**; OIDC = Enterprise | всем, marketplace | всем, experimental |
| Поддержка/стоимость | наша: 1363 строки Go + 815 тестов | Zep | Zep | experimental, свой Docker+БД+LLM-ключи |

## Оценка: что сильнее

**Наше и только наше:** автозахват + автоинъекция через lifecycle-хуки (у Zep этого нет ни в одном продукте, и для их будущего memory-плагина hooks/auto-ingest объявлены out of scope, а сам плагин «not positioned for Claude Code»); redaction секретов до отправки в облако; self-service по API-ключу (их API-ключевой MCP deprecated).

**Их сильные стороны:** scope-параметры поиска богаче (`auto` = optimized context block, `observations`, `thread_summaries`); отдельный `get_user_summary`; enterprise-governance (kill switch, revocation, read-only); нулевая установка. Всё это — за барьером sales + SSO: для соло-разработчика с личным Gmail (не Workspace-доменом) Memory MCP Server фактически недоступен.

## Вердикт

**Полный переход — нет.** Потеряли бы главное (автозахват, автоинъекцию, redaction), а вход — через sales и Google Workspace/Enterprise OIDC. Zep своим ходом скорее подтвердил нишу sentgraph-mcp: официальной памяти для Claude Code у них нет и в v1 не планируется.

**Что взять (по убыванию ценности):**
1. **Scope-параметры поиска** `observations` / `thread_summaries` / `auto` в `memory_search` и `memory_context` — проверить поддержку в zep-go v3.23.0 и пробросить (у нас сейчас только edges/nodes/episodes).
2. **`get_user_summary`**-аналог — нарративное summary пользователя отдельным инструментом (у нас есть только context block через `Thread.GetUserContext` с nil-опциями).
3. **Поставить их Build with Zep plugin** как design-time помощник при разработке самого sentgraph-mcp (живой поиск по докам Zep): `claude plugin marketplace add getzep/building-with-zep-plugin` → `claude plugin install building-with-zep@building-with-zep`. Дополнение, не конкурент.
4. Низкий приоритет: `list_graphs`/directory-ресурс (у нас один граф на проект — не нужно, пока не станет много standalone-графов).

**Следить:** `getzep/zep-memory-plugin` (пустой main, push 2026-08-08) — если Zep передумает про Claude Code и хуки, пересмотреть.
