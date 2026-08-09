# oocla

[![ci](https://github.com/kazufusa/oocla/actions/workflows/ci.yml/badge.svg)](https://github.com/kazufusa/oocla/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/kazufusa/oocla)](https://github.com/kazufusa/oocla/releases)
[![license](https://img.shields.io/github/license/kazufusa/oocla)](LICENSE)

[English](README.md)

oocla を使うと、Ollama API / OpenAI API のクライアントから Claude を
呼び出せます。すべてのリクエストをログイン済みの `claude` CLI 経由で
処理するので、サブスクリプションでも API キーでも、CLI の認証がそのまま
効きます。モデルは `opus` / `sonnet` / `haiku` といった名前で
リクエストできます。名前は OpenAI + Ollama + CLAude に由来します。

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

## oocla の使いどころ

- チャット UI、エディタ、エージェントフレームワークなど、Ollama API /
  OpenAI API 対応のツールは既に数多くあります
- `claude` CLI には、Claude の認証が既に入っています
- oocla はこの 2 つを繋ぎます。単一バイナリで、何も保存せず、認証情報も
  管理しません

## 方針

- ツールは Ollama の JSON tool 定義だけを使います。Claude Code の組み込みツールはすべて無効にします
- Claude Code のデフォルトシステムプロンプトも無効にし、素の LLM として振る舞わせます
- 認証には関与しません。環境の `claude` CLI の認証をそのまま使います
- 会話を保存しません。毎ターン `--no-session-persistence` で実行します
- 標準ライブラリだけで実装し、外部依存を追加しません

## 仕組み

oocla はリクエストを受けるたびに `claude` CLI を 1 プロセス起動し、
応答を返し終えたら捨てます。常駐するモデルも、保存される会話もありません。

```
クライアント (Ollama API / OpenAI API)
  │  リクエスト (会話の全履歴 + ツール定義)
  ▼
oocla serve
  │  履歴を 1 つのプロンプトにまとめ、CLI を起動
  ▼
claude -p --model haiku --tools "" --system-prompt "" --no-session-persistence ...
  │  stream-json イベント (本文 / thinking / tool_use / トークン数)
  ▼
oocla serve
  │  イベントを Ollama / OpenAI それぞれの応答形式に変換
  ▼
クライアント (ストリーミングまたは一括)
```

- どちらの API もステートレスで、クライアントは毎回会話の全履歴を送ってきます。
  oocla はそれを 1 つのターンにまとめて CLI に渡すため、モデルの呼び出しは
  1 リクエストにつき 1 回で済みます。代わりに入力トークンは会話の長さに比例して増えます
- CLI は Claude Code のエージェント機能 (組み込みツール、設定ファイル、
  skill、システムプロンプト) をすべて無効にして起動します。oocla は CLI を、
  素の Claude モデルを呼び出す手段としてだけ使います
- リクエストに tools があるときは、oocla 自身が MCP サーバ (`oocla mcp-shim`) と
  して CLI の子プロセスになり、ツール定義をモデルに見せます。モデルがツールを
  呼ぼうとした時点でターンを打ち切り、`tool_calls` としてクライアントに返します。
  ツールを実行するのはクライアントです
- 起動時に各エイリアスの解決先モデルを CLI に問い合わせ、カタログに
  バージョンを反映します (「モデル名」の節を参照)

設計判断と、根拠にした実測の記録は `docs/DESIGN.ja.md` にあります。

## 必要なもの

必要なのは、認証済みの `claude` CLI が PATH にあることだけです。
oocla 自体はランタイム依存のない単一バイナリで、Go のインストールも要りません。

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
`oocla_<version>_<os>_<arch>` をダウンロードして展開します。

```
$ tar -xzf oocla_1.4.2_linux_amd64.tar.gz
$ ./oocla_1.4.2_linux_amd64/oocla version
v1.4.2
```

Linux / macOS / Windows の amd64 と arm64 を用意しています。
`oocla_<version>_checksums.txt` で検証できます。

### ソースからのビルド

Go 1.25 以上が必要です。

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

`SIGINT` / `SIGTERM` で停止します。停止時は実行中のリクエストを待ってから、
作業用の一時ディレクトリを削除します。

### クライアントの例

Python の `ollama` パッケージから:

```python
from ollama import Client

client = Client(host="http://127.0.0.1:11434")
res = client.chat(model="haiku",
                  messages=[{"role": "user", "content": "Capital of Japan?"}])
print(res["message"]["content"])
```

OpenAI SDK から:

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:11434/v1", api_key="unused")
res = client.chat.completions.create(
    model="sonnet",
    messages=[{"role": "user", "content": "Capital of Japan?"}])
print(res.choices[0].message.content)
```

このほかのツールも、Ollama か OpenAI 互換サーバに繋がるものなら、
接続先を `http://127.0.0.1:11434` に向けるだけで使えます。

## モデル名

| リクエストする名前 | `claude --model` に渡す値 |
| --- | --- |
| `opus`, `sonnet`, `haiku`, `fable` | 同名 |
| 上記に `:latest` またはバージョンタグを付けたもの | エイリアス名 |
| `claude-haiku-4-5-20251001` のようなモデル ID そのもの | そのまま渡す |

起動時に、各エイリアスが実際にどのモデルへ解決されるかを `claude` CLI に
問い合わせ、バージョンをタグにして一覧に載せます。例: `opus:5`、`haiku:4.5`。
問い合わせはモデルを呼ばないゼロターン実行なので、トークンを消費しません。
`opus` や `opus:latest` という指定もそのまま使えます。`/api/show` の
`model_info` には解決先のモデル ID が入ります。それ以外のタグは 404 になります。
バージョンタグは「CLI がいま提供しているもの」の名前であって、
特定リビジョンの固定ではありません。

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

`/api/chat` の空の `messages` と `/api/generate` の空の `prompt` は、Ollama の
流儀どおりプリロード要求として扱います。モデルを呼ばずに `done_reason: "load"`
(`keep_alive: 0` なら `"unload"`) で応答します。実際にロードするものはありません。

### ツール

Ollama と同じく、サーバはツールを実行しません。`tool_calls` を返すので、
クライアントが実行して `role: "tool"` のメッセージで結果を返します。

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

managed settings が MCP サーバの起動を許可しない環境もあります。
その場合は、モデルが応答する前にブロックを検出して別方式に切り替わります。
ツール定義をシステムプロンプトに埋め込み、`--json-schema` で応答の形を
固定する方式です (詳細は `docs/DESIGN.ja.md`)。

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

Ollama の `options` はローカルの推論ランナー向けの設定で、`claude` CLI には
対応する設定がほとんどありません。`stop` だけを実装し、残りは無視して
警告ログに出します。拒否はしません。

### think

| 指定 | 挙動 |
| --- | --- |
| なし / `false` | thinking を返さない |
| `true` | `message.thinking` に載せる |
| `"low"` `"medium"` `"high"` | 載せたうえで推論の深さを指定する |

## 既知の制約

`claude` CLI は既定で、空のシステムプロンプトを指定しても短い固定プロンプトを
差し込みます。加えて、ログイン中のメールアドレスと日付を含む
`<system-reminder>` も入ります (合わせて 170 トークン程度)。
Claude Code 本来のエージェント用プロンプト (数千トークン) は無効化できています。

完全に消すには `oocla serve --bare` を使います。ただしこのモードでは
`claude` CLI が `ANTHROPIC_API_KEY` または apiKeyHelper しか読まなくなり、
OAuth ログインでは動きません。`--agents` / `--safe-mode` などの実測結果も含め、
詳細は `docs/DESIGN.ja.md` にあります。

そのほかに対応しないものは次のとおりです。

- 画像入力。`images` フィールドは受け取りますが使いません
- 埋め込み
- `num_predict` `temperature` `seed` など、`stop` 以外の `options`
- `/api/generate` の `context` による継続。セッションを残さない方針のため、
  受け取っても無視します

最終レスポンスの統計フィールドは、クライアントが Ollama に期待するとおり
常にすべて返します。`load_duration` と `prompt_eval_duration` は
計測対象が存在しないため常に 0 です。

## 開発

```
make check   # gofmt / vet / test -race / build
make e2e     # 実サーバを立てた適合テスト
make dist    # dist/ にリリース成果物を作る
```

`make e2e` は実際に `claude` を呼ぶので課金が発生します。認証済みの環境が必要です。

リリースは `v` で始まるタグを push すると走ります。CI が `make check` を
通してから、`make dist` の成果物をそのまま公開します。ローカルで
`make dist VERSION=v1.2.0` を実行すれば、同じものが手元にできます。
`v1.2.0-rc1` のようにハイフンを含むタグはプレリリース扱いになります。

設計と実測結果は `docs/DESIGN.ja.md` にあります。
