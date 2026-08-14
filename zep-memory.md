# Zep Cloud memory architecture

Sentgraph is a thin Go MCP server and hook layer over Zep Cloud. Zep owns the heavy work: temporal graph construction, entity extraction, deduplication, embeddings, retrieval, and context block assembly.

## What we do not build locally

- No DAG or vertex discovery on our side.
- No local vector database.
- No local graph builder.
- No semantic-search implementation.
- No entity deduplication.

The only local processing before cloud writes is secret redaction and Zep limit enforcement.

## Scope model

- `ZEP_USER_ID` maps to the developer. This user graph stores personal preferences and cross-project facts.
- `SENTGRAPH_PROJECT_ID` maps to one standalone project graph. A project can span many repositories by sharing the same id.
- `thread_id` maps to the agent session id.

## Configuration

Resolved from environment variables; all three identity values are required. The environment wins: keys left unset there are filled from a `.env.local` in the project directory itself (`CLAUDE_PROJECT_DIR`, else the working directory), loaded via godotenv (non-override). Parent directories are never searched, so a file one level up cannot configure unrelated projects. `serve`/`doctor` refuse to run -- and hooks exit quietly -- in a directory that has neither the required keys in its environment nor its own `.env.local`, which keeps the plugin project-scoped and guards against global installs. See `.env.example`.

| Variable | Default | Purpose |
| --- | --- | --- |
| `ZEP_API_KEY` | -- | Required. Zep Cloud API key. |
| `ZEP_USER_ID` | -- | Required. Developer identity for the user graph. |
| `SENTGRAPH_PROJECT_ID` | -- | Required. Project identity for the project graph. |
| `SENTGRAPH_INJECT_EVERY_PROMPT` | `true` | Inject context on every user prompt. |
| `SENTGRAPH_PROJECT_AUTOCAPTURE` | `true` | Auto-capture project facts from hooks. |
| `SENTGRAPH_CAPTURE_TOOLS` | `false` | Persist selected tool outputs on `PostToolUse`. |
| `SENTGRAPH_CONTEXT_TOKEN_BUDGET` | `2000` | Max tokens for assembled context blocks. |

The Zep project graph id is `proj:{project_id}`. Share one project across repositories by setting the same `SENTGRAPH_PROJECT_ID`.

## Core Zep operations

### Add conversation turns

Go SDK:

```go
client.Thread.AddMessages(ctx, threadID, &zep.AddThreadMessagesRequest{
    Messages: []*zep.Message{{Role: "user", Content: "..."}},
    ReturnContext: zep.Bool(true),
})
```

Limits:

- max 30 messages per call
- max 4096 characters per message

`ReturnContext` is important: it lets hooks write the prompt and get fresh context in one call.

### Get user context

Go SDK:

```go
client.Thread.GetUserContext(ctx, threadID, nil)
```

The old `mode` parameter (`summary`/`basic`) is gone. Zep now returns the structured context block by default.

### Add graph data

Go SDK:

```go
client.Graph.Add(ctx, &zep.AddDataRequest{
    GraphID: &projectGraphID,
    Type: zep.GraphDataTypeText,
    Data: "...",
})
```

Use this for project facts, decisions, JSON, and larger non-chat data. Local code chunks payloads above 10000 characters.

### Search graph

Go SDK:

```go
client.Graph.Search(ctx, &zep.GraphSearchQuery{
    GraphID: &projectGraphID,
    Query: "auth decision",
    Scope: zep.GraphSearchScopeEdges.Ptr(),
    Limit: zep.Int(10),
})
```

Use direct search for focused recall and deletion workflows.

## MCP tools

Sentgraph exposes only six core tools:

| Tool | Purpose |
| --- | --- |
| `memory_context` | Get assembled user + project context |
| `memory_search` | Search user/project graph memory |
| `memory_history` | Inspect recent thread messages |
| `memory_add_messages` | Persist conversation turns |
| `memory_add` | Persist durable facts/data |
| `memory_forget` | Delete edge/node/episode by UUID |

Admin CRUD is internal and idempotent: ensure user, ensure project graph, ensure thread.

## Hook cadence

Unlike the old proposal, reading through hooks is supported: Claude hooks can inject context through `hookSpecificOutput.additionalContext`.

Default cadence:

- `SessionStart`: ensure identity, read context, inject it.
- `UserPromptSubmit`: write user prompt, retrieve fresh context, inject it.
- `PreCompact`: read context and inject it before compaction.
- `Stop`: persist the latest assistant turn from transcript.
- `SessionEnd`: final persist pass.

Optional:

- `PostToolUse`: persist selected tool outputs. Default off to avoid noisy memory.

## Skills

Action skills:

- `recall`
- `remember`
- `forget`
- `session-history`

Reference skill:

- `sentgraph-tools` documents all six MCP tools and when to use them.

## Best-practice constraints

- Redact secrets before writing to Zep.
- Prefer `memory_add` only for durable facts, decisions, preferences, and project invariants.
- Do not duplicate routine transcript writes; hooks already capture turns.
- Keep project facts in the project graph and personal preferences in the user graph.
