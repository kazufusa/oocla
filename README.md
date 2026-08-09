# oocla

[![ci](https://github.com/kazufusa/oocla/actions/workflows/ci.yml/badge.svg)](https://github.com/kazufusa/oocla/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/kazufusa/oocla)](https://github.com/kazufusa/oocla/releases)
[![license](https://img.shields.io/github/license/kazufusa/oocla)](LICENSE)

[日本語](README.ja.md)

Use Claude from any Ollama or OpenAI client. oocla is an HTTP server that
speaks both the Ollama API and the OpenAI API and runs every request through
the `claude` CLI — whatever login the CLI already has, subscription or API
key, just works. Request models by names like `opus` / `sonnet` / `haiku`.
The name is OpenAI + Ollama + CLAude.

```
$ oocla serve
oocla listening on 127.0.0.1:11434

$ curl localhost:11434/api/chat -d '{
    "model": "haiku",
    "stream": false,
    "messages": [{"role": "user", "content": "Capital of Japan?"}]
  }'
{"model":"haiku:4.5","message":{"role":"assistant","content":"Tokyo"},
 "done":true,"done_reason":"stop","prompt_eval_count":170,"eval_count":81}

$ curl localhost:11434/v1/chat/completions -d '{
    "model": "haiku",
    "messages": [{"role": "user", "content": "Capital of Japan?"}]
  }'
{"id":"chatcmpl-1","object":"chat.completion","model":"haiku:4.5",
 "choices":[{"index":0,"message":{"role":"assistant","content":"Tokyo"},"finish_reason":"stop"}], ...}
```

## Why oocla

- The tools you already use — chat UIs, editors, agent frameworks — speak
  the Ollama API or the OpenAI API
- The `claude` CLI already holds your Claude login
- oocla connects the two: a single binary that stores nothing and manages no
  credentials

## Principles

- Tools are Ollama's JSON tool definitions only. Claude Code's built-in tools are all disabled
- Claude Code's default system prompt is disabled; the model behaves as a plain LLM
- No involvement in authentication. The environment's `claude` CLI is used with
  whatever credentials it already has
- No conversation is ever stored: every turn runs with `--no-session-persistence`
- Standard library only. No external dependencies

## How it works

oocla starts one `claude` CLI process per request and discards it once the
response is finished. Nothing stays resident and nothing is stored.

```
client (Ollama API / OpenAI API)
  │  request (full conversation history + tool definitions)
  ▼
oocla serve
  │  collapses the history into one prompt, starts the CLI
  ▼
claude -p --model haiku --tools "" --system-prompt "" --no-session-persistence ...
  │  stream-json events (text / thinking / tool_use / token counts)
  ▼
oocla serve
  │  converts the events into the Ollama or OpenAI response shape
  ▼
client (streamed or in one piece)
```

- Both APIs are stateless: the client sends the full history every time.
  oocla collapses it into a single turn for the CLI, so each request costs
  one model call — in return, input tokens grow with the conversation length
- The CLI runs with everything that makes it a coding agent (built-in tools,
  settings files, skills, the system prompt) disabled. oocla uses it only as
  a way to call the plain Claude model
- When a request carries tools, oocla itself becomes the CLI's MCP server
  (`oocla mcp-shim`, a child process) to present the tool definitions to the
  model. The moment the model tries to call one, the turn is cut off and
  returned to the client as `tool_calls`. Executing tools is the client's job
- At startup, oocla asks the CLI what each alias resolves to and reflects the
  version in the catalog (see "Model names")

`docs/DESIGN.md` records the design decisions and the measurements behind them.

## Requirements

An authenticated `claude` CLI on PATH. That is all.
oocla itself is a single binary with no runtime dependencies; Go is not needed.

### Install

Homebrew (macOS or Linux):

```
$ brew install kazufusa/tap/oocla
```

With a Go toolchain:

```
$ go install github.com/kazufusa/oocla/cmd/oocla@latest
```

Or download `oocla_<version>_<os>_<arch>` from the
[releases page](https://github.com/kazufusa/oocla/releases) and unpack it.

```
$ tar -xzf oocla_1.4.2_linux_amd64.tar.gz
$ ./oocla_1.4.2_linux_amd64/oocla version
v1.4.2
```

amd64 and arm64 builds are provided for Linux / macOS / Windows.
Verify with `oocla_<version>_checksums.txt`.

### Build from source

Requires Go 1.25 or later.

```
$ git clone https://github.com/kazufusa/oocla
$ cd oocla && make build   # produces bin/oocla
```

## Usage

```
oocla serve [options]
```

| Option | Default | Description |
| --- | --- | --- |
| `--addr` | `127.0.0.1:11434` | Address to listen on |
| `--claude` | `claude` | Path to the `claude` executable |
| `--bare` | `false` | Removes everything the CLI injects, but requires API key auth (see below) |

Stops on `SIGINT` / `SIGTERM`. On shutdown it waits for in-flight requests,
then removes its scratch working directory.

### Client examples

With the `ollama` Python package:

```python
from ollama import Client

client = Client(host="http://127.0.0.1:11434")
res = client.chat(model="haiku",
                  messages=[{"role": "user", "content": "Capital of Japan?"}])
print(res["message"]["content"])
```

With the OpenAI SDK:

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:11434/v1", api_key="unused")
res = client.chat.completions.create(
    model="sonnet",
    messages=[{"role": "user", "content": "Capital of Japan?"}])
print(res.choices[0].message.content)
```

Anything else that talks to Ollama or to an OpenAI-compatible server can be
pointed at `http://127.0.0.1:11434` the same way.

## Model names

| Requested name | Value passed to `claude --model` |
| --- | --- |
| `opus`, `sonnet`, `haiku`, `fable` | Same name |
| Any of the above with `:latest` or its version tag | The bare alias |
| An exact model id like `claude-haiku-4-5-20251001` | Passed through as is |

At startup oocla asks the `claude` CLI what each alias resolves to (a
zero-turn run: no tokens are spent) and advertises the version as the tag,
e.g. `opus:5` or `haiku:4.5`. `opus` and `opus:latest` keep working, and
`/api/show` reports the resolved id in `model_info`. Any other tag is a 404:
the version tag names what the CLI serves today, not a pinned revision.

## Endpoints

| Endpoint | Description |
| --- | --- |
| `POST /api/chat` | Streaming / non-streaming. tools, format, think, options.stop |
| `POST /api/generate` | Streaming / non-streaming |
| `GET /api/tags` `POST /api/show` `GET /api/version` | Model catalog |
| `GET /api/ps` | Always empty. No model is ever resident in memory |
| `POST /api/pull` | Succeeds for known models. There is nothing to transfer |
| `POST /v1/chat/completions` | OpenAI-compatible. SSE supported |
| `GET /v1/models` `GET /v1/models/{model}` | OpenAI-compatible |
| `POST /api/embeddings` `/api/embed` `/v1/embeddings` | 501. The `claude` CLI has no embedding support |
| `POST /api/create` `/api/copy` `/api/push`, `DELETE /api/delete` | 400. No local model files exist |

An empty `messages` array on `/api/chat`, like an empty `prompt` on
`/api/generate`, is Ollama's preload call: it is acknowledged with
`done_reason: "load"` (`"unload"` with `keep_alive: 0`) without invoking the
model. There is nothing to actually load.

### Tools

As with Ollama, the server never executes tools. It returns `tool_calls`; the
client executes them and sends the results back as `role: "tool"` messages.

```
$ curl localhost:11434/api/chat -d '{
    "model": "haiku", "stream": false,
    "messages": [{"role": "user", "content": "What is the weather in Tokyo?"}],
    "tools": [{"type": "function", "function": {
      "name": "get_weather",
      "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}}}]
  }' | jq .message.tool_calls
[{"function":{"name":"get_weather","arguments":{"city":"Tokyo"}}}]
```

#### Environments where MCP is unavailable

Where managed settings refuse to start the MCP server, oocla detects it
before the model answers and switches automatically: tool definitions go
into the system prompt and `--json-schema` pins the answer shape (see
`docs/DESIGN.md` for details).

| Option | Description |
| --- | --- |
| `--internal-mcp-shim-name` | Register the MCP server under the name your administrator allowlisted for oocla |
| `--internal-prompt-tools` | Never use MCP; always use the prompt-based mode above (debugging) |

Measured against the real models (`scripts/verify-prompt-tools.sh`, 2026-07):

| Model | Tool call | Declining tools | Answer from result |
| --- | --- | --- | --- |
| opus | 5/5 | 2/2 | 3/3 |
| sonnet | 5/5 | 2/2 | 3/3 |
| haiku | 5/5 | 2/2 | 3/3 |
| fable | 5/5 | 2/2 | 3/3 |

### options

Ollama's `options` configure a local inference runner, and most of them have
no counterpart in the `claude` CLI. Only `stop` is implemented; the rest are
ignored with a logged warning. Nothing is rejected.

### think

| Value | Behaviour |
| --- | --- |
| absent / `false` | Thinking is not returned |
| `true` | Delivered in `message.thinking` |
| `"low"` `"medium"` `"high"` | Delivered, and the reasoning depth is set to the given level |

## Known limitations

By default the `claude` CLI injects a short fixed prompt plus a
`<system-reminder>` carrying the logged-in email address and the date, even
with an empty system prompt (about 170 tokens in total). Claude Code's full
agent prompt (thousands of tokens) is successfully disabled.

To remove everything, use `oocla serve --bare`. In that mode the `claude` CLI
only reads `ANTHROPIC_API_KEY` or an apiKeyHelper, so an OAuth login no longer
works. See `docs/DESIGN.md` for details, including measured results for
`--agents` / `--safe-mode` and friends.

Also unsupported:

- Image input. The `images` field is accepted but unused
- Embeddings
- Any `options` other than `stop`, e.g. `num_predict` `temperature` `seed`
- Continuation via `context` on `/api/generate`: it is accepted but ignored,
  since no session is ever kept

The final response always carries every statistics field, as clients expect
from Ollama. `load_duration` and `prompt_eval_duration` are always 0: there
is no model load or local prompt evaluation to time.

## Development

```
make check   # gofmt / vet / test -race / build
make e2e     # conformance tests against a real server
make dist    # builds release artifacts into dist/
```

`make e2e` calls the real `claude` and costs money. It needs an authenticated
environment.

A release runs when a tag starting with `v` is pushed. CI runs `make check`
and then publishes the `make dist` artifacts unchanged. Running
`make dist VERSION=v1.2.0` locally produces the same files. Tags containing a
hyphen, like `v1.2.0-rc1`, become prereleases.

Design and measured behaviour live in `docs/DESIGN.md`.

## License

MIT
