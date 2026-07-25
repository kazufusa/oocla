# oocla

[English](README.md)

Ollama API と OpenAI API の両方に対応した HTTP サーバを立て、バックエンドで
`claude` CLI を呼ぶ。どちらの API のクライアントからも
`opus` / `sonnet` / `haiku` といったモデル名でリクエストできる。
名前は OpenAI + Ollama + CLAude から。

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

## 方針

- ツールは Ollama の JSON tool 定義のみ。Claude Code の組み込みツールはすべて無効にする
- Claude Code のデフォルトシステムプロンプトも無効にする。素の LLM として振る舞う
- 認証には関与しない。環境の `claude` CLI の認証をそのまま使う
- 会話を保存しない。毎ターン `--no-session-persistence` で実行する
- 標準ライブラリのみ。外部依存を追加しない

## 必要なもの

認証済みの `claude` CLI が PATH にあること。それだけ。
oocla 自体はランタイム依存なしの単一バイナリで、Go のインストールも要らない。

### インストール

Homebrew (macOS / Linux どちらでも):

```
$ brew install kazufusa/tap/oocla
```

Go が入っていれば:

```
$ go install github.com/kazufusa/oocla/cmd/oocla@latest
```

もしくは [リリース](https://github.com/kazufusa/oocla/releases) から
`oocla_<version>_<os>_<arch>` をダウンロードして展開する。

```
$ tar -xzf oocla_1.1.0_linux_amd64.tar.gz
$ ./oocla_1.1.0_linux_amd64/oocla version
v1.1.0
```

Linux / macOS / Windows の amd64 と arm64 を用意している。
`oocla_<version>_checksums.txt` で検証できる。

### ソースからビルド

Go 1.25 以上が必要。

```
$ git clone https://github.com/kazufusa/oocla
$ cd oocla && make build   # bin/oocla ができる
```

## 使い方

```
oocla serve [オプション]
```

| オプション | 既定値 | 内容 |
| --- | --- | --- |
| `--addr` | `127.0.0.1:11434` | 待ち受けアドレス |
| `--claude` | `claude` | `claude` 実行ファイルのパス |
| `--bare` | `false` | CLI が自動で差し込むプロンプトを完全に消す。ただし API キー認証が必須になる (後述) |

`SIGINT` / `SIGTERM` で停止する。停止時は実行中のリクエストを待ってから
作業用の一時ディレクトリを削除する。

毎ターン `--no-session-persistence` で `claude` を実行するので、
会話がディスクに残ることはない。代わりに、マルチターンの会話は毎回
全履歴を1つのターンにまとめて送るため、入力トークンが会話の長さに比例して増える。

## モデル名

| リクエストする名前 | `claude --model` に渡す値 |
| --- | --- |
| `opus`, `sonnet`, `haiku`, `fable` | 同名 |
| 上記に `:latest` を付けたもの | 同名 |
| `claude-haiku-4-5-20251001` のようなモデル ID そのもの | そのまま渡す |

`:latest` 以外のタグは 404 になる。特定リビジョンを提供する手段がないため。

## 対応エンドポイント

| エンドポイント | 内容 |
| --- | --- |
| `POST /api/chat` | ストリーミング / 非ストリーミング。tools, format, think, options.stop |
| `POST /api/generate` | ストリーミング / 非ストリーミング |
| `GET /api/tags` `POST /api/show` `GET /api/version` | モデルカタログ |
| `GET /api/ps` | 常に空。メモリに常駐するモデルは存在しない |
| `POST /api/pull` | 既知のモデルなら成功を返す。転送するものはない |
| `POST /v1/chat/completions` | OpenAI 互換。SSE 対応 |
| `GET /v1/models` `GET /v1/models/{model}` | OpenAI 互換 |
| `POST /api/embeddings` `/api/embed` `/v1/embeddings` | 501。`claude` CLI に埋め込み機能がない |
| `POST /api/create` `/api/copy` `/api/push`, `DELETE /api/delete` | 400。ローカルにモデル実体がない |

### ツール

Ollama と同じく、サーバはツールを実行しない。`tool_calls` を返すので、
クライアントが実行して `role: "tool"` のメッセージで結果を返す。

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

#### MCP を使えない環境

managed settings が MCP サーバの起動を許可しない環境では、tools 付きリクエストを
応答前に検出し、ツール定義をシステムプロンプトに埋め込んで `--json-schema` で
応答の形を固定する方式に自動で切り替わる (詳細は `docs/DESIGN.md`)。

| オプション | 内容 |
| --- | --- |
| `--internal-mcp-shim-name` | 管理者が oocla 用に許可リストへ登録した名前で MCP サーバを登録する |
| `--internal-prompt-tools` | MCP を使わず常に上記の方式で動かす (デバッグ用) |

実モデルでの計測 (`scripts/verify-prompt-tools.sh`、2026-07):

| モデル | ツール呼び出し | ツールを使わない判断 | 結果を受けて回答 |
| --- | --- | --- | --- |
| opus | 5/5 | 2/2 | 3/3 |
| sonnet | 5/5 | 2/2 | 3/3 |
| haiku | 5/5 | 2/2 | 3/3 |
| fable | 5/5 | 2/2 | 3/3 |

### options

Ollama の `options` はローカルの推論ランナーを設定するためのもので、
`claude` CLI には対応する設定がほとんどない。`stop` だけ実装し、
残りは無視して警告ログに出す。拒否はしない。

### think

| 指定 | 挙動 |
| --- | --- |
| なし / `false` | thinking を返さない |
| `true` | `message.thinking` に載せる |
| `"low"` `"medium"` `"high"` | 載せたうえで推論の深さを指定する |

## 既知の制約

`claude` CLI は既定で、空のシステムプロンプトを指定しても短い固定プロンプトと、
ログイン中のメールアドレスと日付を含む `<system-reminder>` を差し込んでくる
(合わせて170トークン程度)。Claude Code 本来のエージェント用プロンプト
(数千トークン) は無効化できている。

完全に消すには `oocla serve --bare` を使う。ただしこのモードでは `claude` CLI が
`ANTHROPIC_API_KEY` または apiKeyHelper しか読まなくなり、OAuth ログインでは動かない。
`--agents` / `--safe-mode` などを実測した結果も含め、詳細は `docs/DESIGN.md`。

その他、対応しないもの。

- 画像入力。`images` フィールドは受け取るが使わない
- 埋め込み
- `num_predict` `temperature` `seed` など、`stop` 以外の `options`
- `/api/generate` の `context` による継続。セッションを残さない方針のため、
  受け取るが無視する

## 開発

```
make check   # gofmt / vet / test -race / build
make e2e     # 実サーバを立てた適合テスト
make dist    # dist/ にリリース成果物を作る
```

`make e2e` は実際に `claude` を呼ぶので課金が発生する。認証済みの環境が必要。

リリースは `v` で始まるタグを push すると走る。CI が `make check` を通してから
`make dist` の成果物をそのまま公開する。`make dist VERSION=v1.1.0` をローカルで
実行すれば同じものが手元にできる。`v1.1.0-rc1` のようにハイフンを含むタグは
プレリリース扱いになる。

設計と実測結果は `docs/DESIGN.md`。
