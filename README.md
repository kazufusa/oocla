# oocla

[日本語](README.ja.md)

An HTTP server that speaks both the Ollama API and the OpenAI API, backed by
the `claude` CLI. Clients of either dialect can request models by names like
`opus` / `sonnet` / `haiku`. The name is OpenAI + Ollama + CLAude.

```
$ oocla serve
oocla listening on 127.0.0.1:11434

$ curl localhost:11434/api/chat -d '{
    "model": "haiku",
    "stream": false,
    "messages": [{"role": "user", "content": "Capital of Japan?"}]
  }'
{"model":"haiku:latest","message":{"role":"assistant","content":"Tokyo"},
 "done":true,"done_reason":"stop","prompt_eval_count":170,"eval_count":81}

$ curl localhost:11434/v1/chat/completions -d '{
    "model": "haiku",
    "messages": [{"role": "user", "content": "Capital of Japan?"}]
  }'
{"id":"chatcmpl-1","object":"chat.completion","model":"haiku:latest",
 "choices":[{"index":0,"message":{"role":"assistant","content":"Tokyo"},"finish_reason":"stop"}], ...}
```

## Principles

- Tools are JSON tool definitions only. Claude Code's built-in tools are all disabled
- Claude Code's default system prompt is disabled. It behaves as a plain LLM
- No involvement in authentication. Whatever the environment's `claude` CLI is
  authenticated as is inherited verbatim
- No conversation is ever stored: every turn runs with `--no-session-persistence`
- Standard library only. No external dependencies

## Requirements

An authenticated `claude` CLI on PATH. That is all.
oocla itself is a single binary with no runtime dependencies; Go is not needed.

### Install

Homebrew, on macOS and Linuxbrew alike:

```
$ brew install kazufusa/tap/oocla
```

With a Go toolchain:

```
$ go install github.com/kazufusa/oocla/cmd/oocla@latest
```

Or download `oocla_<version>_<os>_<arch>` from the
[releases](https://github.com/kazufusa/oocla/releases) and unpack it.

```
$ tar -xzf oocla_1.0.0_linux_amd64.tar.gz
$ ./oocla_1.0.0_linux_amd64/oocla version
v1.0.0
```

amd64 and arm64 builds are provided for linux / macOS / Windows.
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
| `--bare` | `false` | Removes every injection, but requires API key auth (see below) |

Stops on `SIGINT` / `SIGTERM`. On shutdown it waits for in-flight requests,
then removes its scratch working directory.

Every turn runs `claude` with `--no-session-persistence`, so no conversation
is ever written to disk. In exchange, a multi-turn conversation resends its
whole history folded into one turn, so input tokens grow with the length of
the conversation.

## Model names

| Requested name | Value passed to `claude --model` |
| --- | --- |
| `opus`, `sonnet`, `haiku`, `fable` | Same name |
| Any of the above with `:latest` | Same name |
| An exact model id like `claude-haiku-4-5-20251001` | Passed through as is |

Any tag other than `:latest` is a 404: there is no way to serve a pinned
revision.

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
| `POST /api/embeddings` `/api/embed` `/v1/embeddings` | 501. The `claude` CLI has no embeddings |
| `POST /api/create` `/api/copy` `/api/push`, `DELETE /api/delete` | 400. No local model files exist |

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

### options

Ollama's `options` configure a local inference runner, and the `claude` CLI has
almost no matching entry points. Only `stop` is implemented; the rest are
ignored with a warning log. Nothing is refused.

### think

| Value | Behaviour |
| --- | --- |
| absent / `false` | Thinking is not returned |
| `true` | Delivered in `message.thinking` |
| `"low"` `"medium"` `"high"` | Delivered, with the reasoning depth set |

## Known limitations

By default the `claude` CLI injects a base prompt plus a `<system-reminder>`
carrying the logged-in email address and the date, even with an empty system
prompt (about 170 tokens in total). Claude Code's real agent prompt (thousands
of tokens) is successfully disabled.

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

## Development

```
make check   # gofmt / vet / test -race / build
make e2e     # conformance tests against a real server
make dist    # builds release artifacts into dist/
```

`make e2e` calls the real `claude` and costs money. It needs an authenticated
environment.

A release runs when a tag starting with `v` is pushed. CI runs `make check`
and then publishes the `make dist` artifacts as they are. Running
`make dist VERSION=v1.0.0` locally produces the same files. Tags containing a
hyphen, like `v1.0.0-rc1`, become prereleases.

Design and measured behaviour live in `docs/DESIGN.md`.
