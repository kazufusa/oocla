# oocla design

[日本語](DESIGN.ja.md)

An HTTP server that speaks both the Ollama API and the OpenAI API, backed by
the `claude` CLI. The goal is to look like Ollama itself to Ollama clients,
and like an OpenAI-compatible server to OpenAI clients.

## Goals and non-goals

Goals

- Ollama clients connect to `http://localhost:11434` unchanged
- OpenAI clients connect unchanged too, via `/v1/chat/completions` and `/v1/models`
- Models can be requested by names like `opus` / `sonnet` / `haiku`
- Tools are Ollama's JSON tool definitions only; all Claude Code built-in tools are disabled
- Claude Code's default system prompt is disabled as well; the model behaves as a plain LLM

Non-goals

- Authentication. The `claude` CLI's own credentials are used as-is. oocla
  handles neither API keys nor OAuth, and passes no auth-related flags
- Conversation storage. Every turn runs with `--no-session-persistence` and
  leaves nothing on disk (see below)
- Embeddings (`/api/embeddings`). The `claude` CLI has no equivalent, so 501
- Model pull / push / delete. No local model files exist

## Measured CLI behaviour

Facts confirmed with `claude` 2.1.217. The implementation assumes nothing
beyond what is written here.

| Requirement | Mechanism | Confirmed result |
| --- | --- | --- |
| Disable all built-in tools | `--tools ""` | `tools` in the init event becomes `[]` |
| Disable the default prompt | `--system-prompt <s>` | Replaced. About 180 `input_tokens` even for an empty prompt |
| Ignore settings files | `--setting-sources ""` | CLAUDE.md / settings.json are not read |
| Disable skills | `--disable-slash-commands` | `slash_commands` in init is `[]` |
| Restrict MCP | `--strict-mcp-config --mcp-config <json>` | Only the specified MCP servers |
| Structured output | `--json-schema <schema>` | Mapped to Ollama's `format` |
| Streaming | `--output-format stream-json --verbose` | Events flow as NDJSON |
| Token-by-token output | `--include-partial-messages` | Partial chunks flow |
| Alias resolution | `--max-turns 0` | Exits without calling the API; the init event carries the resolved model id |

`--bare` is not used: it restricts authentication to `ANTHROPIC_API_KEY` /
apiKeyHelper and breaks OAuth environments. Measurements showed normal
responses without `--bare` even with `apiKeySource: "none"`.

### Empty system prompt

Even when the Ollama request has no system message, `--system-prompt ""` is
always passed. The CLI treats the empty string as "an empty system prompt"
rather than "unspecified", which removes the default.

| Given | `input_tokens` |
| --- | --- |
| `--system-prompt ""` | 170 |
| `--system-prompt "You are a helpful assistant."` | 176 |

170 tokens is the lower bound of the CLI's overhead. Claude Code's full agent
system prompt (thousands of tokens) is not present.

### Auto-injected content that cannot be removed (known limitation)

Even with `--system-prompt ""`, the following stays in the request. Confirmed
by having the model repeat it back.

```
You are a Claude agent, built on Anthropic's Claude Agent SDK.

<system-reminder>
As you answer the user's questions, you can use the following context:
# userEmail
The user's email address is <logged-in email address>
# currentDate
Today's date is <date>
...
</system-reminder>
```

So the model is not a plain LLM but a "minimal Claude agent". In addition,
the logged-in email address and the date ride along on every request.

Attempts and their results:

| Attempt | Result |
| --- | --- |
| `--system-prompt ""` | The fixed prompt and the system-reminder remain |
| `--agents` + `--agent` | Only appended after the fixed prompt. Remains |
| `--safe-mode` | Identical `input_tokens` (184). No change |
| `--exclude-dynamic-system-prompt-sections` | Same. Ignored when `--system-prompt` is present |
| `--bare` / `CLAUDE_CODE_SIMPLE=1` | Gone, at the cost described below |

Only `--bare` actually removes it. But it restricts authentication to
`ANTHROPIC_API_KEY` / apiKeyHelper, and OAuth environments fail with
`Not logged in · Please run /login`.

It is off by default and takes effect only when chosen explicitly with
`oocla serve --bare`. Even in this mode oocla stays out of authentication: it
only passes the flag, and the CLI's own rules decide whether auth succeeds.
In API-key environments this removes the injected content completely.

### Working directory

The CLI reads CLAUDE.md and project memory from the cwd. Running it in the
directory where oocla was started would leak those into the conversation, so
a dedicated empty directory is created and used instead.

### stream-json input behaviour

Feeding `{"type":"assistant",...}` lines into `--input-format stream-json`
does work as history. But every `{"type":"user",...}` line runs one turn, and
a `result` event fires per line. Replaying the history each time therefore
multiplies API calls by the history length.

## Architecture

```
oocla (single binary)
├── serve      : Ollama / OpenAI-compatible HTTP server (:11434)
├── mcp-shim   : child process exposing the request's tools as MCP tools
└── version
```

### Package layout

The Ollama and OpenAI APIs share no schema, and their paths split cleanly
into `/api/*` and `/v1/*`. So each API gets its own package, with the shared
machinery below them. The two API implementations do not depend on each other.

```
cmd/oocla         Ties the two together. /v1/ → openai, everything else → ollama
internal/ollama   The Ollama API. Only the /api/* request/response JSON types plus parsing/encoding
internal/openai   The OpenAI API. Only the /v1/* request/response JSON types plus parsing/encoding
internal/core     The core, independent of both APIs: conversation model (Message / Tool),
                  model catalog, history collapsing, stop handling, Engine (execution)
internal/httpapi  A small HTTP router (405 with Allow) and JSON helpers
internal/bridge   The core.Generator implementation. Calls the claude CLI once
```

Invariants (all pinned by `internal/core/boundary_test.go`):

- `internal/openai` and `internal/ollama` never import each other
- Only `cmd/oocla` may import those two. bridge / mcpshim / claudecli /
  httpapi depend on core types only
- Each API implementation converts its own request/response types into the
  core intermediate representation (`core.ChatSpec`) and hands that to
  `core.Engine`. One API's types never ride the other's conversion path
- Error shapes are decided per API. `/v1/*` returns even 404 / 405 in
  OpenAI's nested format
- `core.Message`'s JSON tags are exactly Ollama's request/response JSON.
  Ollama's format maps 1:1 to the intermediate representation, so the Ollama
  side uses it directly and the OpenAI side converts. `core.Tool` is shared
  by both APIs, because Ollama adopted OpenAI's shape as-is

```
Ollama client
   │  POST /api/chat  (full messages history + tools)
   ▼
oocla serve
   │  collapses the history into one user turn (one API call)
   ▼
claude -p --model haiku --tools "" --system-prompt ... --no-session-persistence
   │
   └── (with tools) --strict-mcp-config --mcp-config → oocla mcp-shim
```

### No sessions

Every turn runs with `--no-session-persistence`. The `claude` CLI leaves
nothing on disk, and oocla remembers nothing about the conversation.

Both APIs are stateless: the client sends the full history every time. oocla
collapses it into a single user turn per request, so input tokens grow with
the conversation length. That is the accepted price of keeping nothing.

### Tools

Ollama tool calls are never executed server-side. The server returns
`tool_calls`; the client executes them and sends the results back as
`role: "tool"` messages.

To make the `claude` CLI do this, the request's `tools` are exposed as MCP
tools through `oocla mcp-shim`. The shim is a subcommand of oocla itself, so
nothing extra needs to be installed. Tool definitions are passed through the
`OOCLA_TOOLS` environment variable.

Tools are not executed. The moment the model emits a `tool_use` block, the
parent process cuts the turn off and builds the HTTP response as
`tool_calls`. `tool_use` flows through the output stream before execution,
so the shim's `tools/call` is normally never invoked. If it is reached
anyway, the shim returns an error saying that tool results come from the API
client — returning a fake result would make the model reason on top of it.

A side effect of the cut-off is that no `result` event arrives, so turns
that end in a tool call cannot use the CLI's own accounting. Instead, token
counts are assembled from two sources found by measurement. Input tokens
come from the usage snapshot on the assistant event; the input side is
already final at request time, so this is exact. For output tokens,
`--include-partial-messages` is always added on turns with tools or
structured output. The `message_delta` event at the end of the message
carries the final output token count, and the CLI moves on to tool execution
only after the message closes — so reading up to `message_delta` before
cutting off yields the exact number at no extra cost.

Only the duration exists solely in `result`, so cut-off turns cannot report it.

The CLI has a child process (the MCP shim), so the cut-off kills the whole
process group. Killing only the CLI would leave the shim holding the output
pipe open.

The model sees tool names as `mcp__oocla__<name>`. The prefix is stripped
back to the original name in the response's `tool_calls`, but the model may
still mention the prefixed name in its text.

#### Fallback for environments where MCP is blocked

Under managed settings (measured: an `allowedMcpServers` allowlist), the CLI
does not start oocla's MCP server. In that case `mcp_servers` in the init
event comes up empty, which is where it is detected; the turn is cut off
immediately and retried by other means. The cut-off happens before the model
answers, so only the one interrupted call is wasted.

Detection also runs once at startup to print a warning. Measurement
confirmed that `claude mcp list` drops servers whose names are not allowed,
so a `.mcp.json` declaring the shim is placed in the scratch directory to
see whether it appears in the list. No model is called, so nothing is
billed. If the managed-settings file is readable, allowed names usable as
the shim name are attached to the warning as candidates (a best-effort
direct file read; registry or server-distributed policies are invisible).
The allowlist may also contain URL-style entries like `http://…`, but
measurement showed the CLI itself rejects a stdio server under such a name
even when policy allows it, so those are excluded from the candidates.
Request-time detection is the real mechanism; the startup probe is only
advance notice.

The retry embeds the tool definitions into the system prompt and forces a
`{content, tool_calls}` response shape with `--json-schema`. A non-empty
`tool_calls` is returned as a normal tool call; when it is empty, `content`
becomes the answer text. Less robust than going through MCP, but better than
answering in plain text without telling anyone.

In measurements, model responses split two ways: filling in `tool_calls` as
instructed, or trying to call the prompted tool name directly with a
`tool_use` block. The CLI streams the latter as events too, so both are
accepted as the same request.

Every schema field gets a description; measurement showed this is required.
Without descriptions, haiku interpreted `tool_calls` as "a record of past
calls" and, on the turn after receiving a tool result, filled in the same
call again — reproducible 3/3. (With thinking enabled it correctly decided
to report the result; only the mapping onto the response shape was broken.)
Writing "these are calls to execute now, not a record" in the description
turned that into 3/3 success.

Per-model scores (`scripts/verify-prompt-tools.sh`, measured 2026-07):

| Model | Tool call | Declining tools | Answer from result |
| --- | --- | --- | --- |
| opus | 5/5 | 2/2 | 3/3 |
| sonnet | 5/5 | 2/2 | 3/3 |
| haiku | 5/5 | 2/2 | 3/3 |
| fable | 5/5 | 2/2 | 3/3 |

With `oocla serve --internal-prompt-tools`, MCP is never used and this mode
is always on — meant for debugging the fallback. In environments where MCP
is permanently blocked, it also saves the one interrupted detection call per
request.

Constraint: this response shape occupies `--json-schema`, so tools cannot be
combined with `format` (structured output / JSON mode) in this environment.
That case returns an error stating the reason; tools are never silently
dropped.

For environments where an administrator has allowlisted a different name for
oocla, `oocla serve --internal-mcp-shim-name <name>` changes the shim's
registration name. The MCP config, `--allowedTools`, and tool-name prefix
stripping all follow it. This exists to match a name issued by the
administrator, not to impersonate other allowed servers; the proper route is
getting oocla itself allowlisted.

### Model names

Both APIs accept the same names.

| Client side | Value passed to `--model` |
| --- | --- |
| `opus`, `opus:latest`, `opus:5` (resolved version) | `opus` |
| `sonnet`, `haiku`, `fable` and their tagged forms | Same name |
| Names starting with `claude-` | Passed through |

Only two tags are accepted: `latest` and the currently resolved version.
Anything else is a 404, since there is no way to serve a specific revision.

`/api/tags` returns this catalog. With no local files, `size` and `digest`
are derived deterministically from the model name.

#### Version resolution (startup probe)

The CLI resolves aliases to real model ids with a local table. A zero-turn
run with `--max-turns 0` exits without calling the API, announcing the
resolved id (such as `claude-opus-5`) in the init event (measured: about 2
seconds).

At startup this run happens once per alias, and the version read from the id
becomes the catalog tag (`opus:5`, `haiku:4.5`). `/api/show` carries
`general.version` and the resolved id (`claude.resolved_model`) in
`model_info`.

Probes run in the background, in parallel. Until one completes, the entry
keeps answering as `:latest`; failed entries simply stay `:latest`. Probe
results never make the catalog worse.

### Ollama compatibility details

Decisions matched against client implementations (a real example:
strands-agents' Ollama provider) and Ollama's actual behaviour.

- Terminal responses (a non-streaming response, and the final line of a
  stream) always carry all six statistics fields (`total_duration`
  `load_duration` `prompt_eval_count` `prompt_eval_duration` `eval_count`
  `eval_duration`), even when a value is 0. Real Ollama always fills them,
  so clients read them without existence checks. If one is missing, strands,
  for instance, crashes on a None reference when adding
  `prompt_eval_count + eval_count`
- Mid-stream chunks, conversely, carry no statistics fields. Ollama's chunks
  do not either
- `load_duration` and `prompt_eval_duration` are always 0, because there is
  nothing to time
- An empty `messages` (`/api/chat`) or an empty `prompt` (`/api/generate`)
  is a preload request. Ollama reads it as "load the model into memory" and
  responds without generating. oocla has nothing to load, but answers in the
  same shape: no model call, `done_reason: "load"`, or `"unload"` when
  `keep_alive` is 0 (numeric, or a string like `"0s"`). Returning an error
  instead would make clients that send a warm-up before chatting decide the
  model is unusable and stop

## Harness

"Green" is defined as `make check` passing.

| Command | Description |
| --- | --- |
| `make fmt` | `gofmt -l` confirms an empty diff |
| `make vet` | `go vet ./...` |
| `make test` | `go test -race ./...` |
| `make build` | `go build ./...` |
| `make check` | All four above |
| `make e2e` | Boots the server and runs the conformance tests. Needs an authenticated `claude` |

Standard library only; no external dependencies.

## Structured output

`--json-schema` is implemented inside the CLI as a built-in tool named
`StructuredOutput`. The model writes some text, then calls this tool, and
the arguments are the structured answer. This behaviour was established by
measurement, so oocla handles it as follows.

- With a schema, the `StructuredOutput` call is treated as the answer and
  never surfaced in `tool_calls`
- The preceding text is preamble and gets dropped; Ollama returns no text
  either
- Streaming also drops the preamble and emits the final JSON as one chunk

`format: "json"` (no schema) does not use `--json-schema`: with nothing to
constrain, the CLI wraps the answer in an `{"output": "..."}` object.
Instead, one sentence is appended to the system prompt. This is the only
place where oocla ever adds text to a prompt, and only when the client
explicitly asked for JSON mode.

The instruction alone cannot fully constrain the model, which sometimes
wraps the answer in a ```json fence. JSON mode strips the fence — only when
the stripped result is valid JSON.

## thinking

The CLI has no way to stop reasoning; the model always thinks. So all
`think` controls is whether the client gets to see it. This also matches
Ollama's spec: thinking never appears in a response unless requested.

| Request | Behaviour |
| --- | --- |
| `think` absent / `false` | Thinking is discarded, not collected |
| `think: true` | Delivered in `message.thinking`; effort is the model default |
| `think: "low"` / `"medium"` / `"high"` | Delivered, and `--effort <level>` is passed |

`"xhigh"` and `"max"` are passed through too, since the CLI accepts them.
They are not in Ollama's spec, but can arrive from clients written with
Claude in mind.

## options

Ollama's `options` configure a local llama.cpp runner, and the `claude` CLI
has almost no counterparts. They are not rejected: they are ignored with a
logged warning. Clients send an options block whether or not they care about
its contents, and Ollama itself ignores options a model does not implement.

`stop` alone is implemented. Sampling cannot be stopped, so the output is
cut at the first stop sequence. When streaming, a tail that might still be
mid-sequence is held back, so a sequence spanning chunk boundaries is not
missed. On a false positive (the `EN` was `ENOUGH`, not `END`), the held
tail is emitted as-is.

## OpenAI compatibility layer

Ollama serves an OpenAI-compatible endpoint family alongside its own API,
and clients written with the OpenAI SDK use that. oocla provides the same
route.

| Path | Description |
| --- | --- |
| `POST /v1/chat/completions` | The main endpoint. SSE streaming supported |
| `GET /v1/models` | Model list |
| `GET /v1/models/{model}` | Single model |

`internal/openai` converts its request/response types directly into
`core.ChatSpec` and feeds the same `core.Engine`; the Ollama types are never
involved. Only the differences:

- `stream` defaults to false. The Ollama side defaults to true
- Tool arguments are JSON strings, not objects
- Tool results reference calls by `tool_call_id`; the tool name is recovered
  from the preceding `tool_calls`
- `content` accepts both a string and an array of typed parts; non-text
  parts are 400
- The `developer` role is treated as system
- `reasoning_effort` maps to the same effort levels as `think`
- Errors use the nested `{"error":{"message":...,"type":...}}` shape, unlike
  Ollama's flat one
- Thinking is delivered in a `reasoning` field, since OpenAI has no
  equivalent

## Cleanup

The working directory is a temporary directory that oocla created. On a
deliberate shutdown via SIGINT / SIGTERM, in-flight requests are awaited and
then the directory is removed.

As long as `--no-session-persistence` is passed, the CLI should write no
transcripts. As insurance, `~/.claude/projects/<encoded cwd>/`, derived from
the working directory, is also removed at shutdown — and it is only derived
when the directory name contains `oocla-cwd-`.
