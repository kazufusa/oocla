# oocla 設計

Ollama API と OpenAI API の両方に対応した HTTP サーバを立て、バックエンドとして
`claude` CLI を呼ぶ。Ollama クライアントには Ollama そのものに、OpenAI
クライアントには OpenAI 互換サーバに見えることを目標にする。

## 目標と非目標

目標

- `http://localhost:11434` に対して Ollama クライアントがそのまま繋がる
- `/v1/chat/completions` `/v1/models` で OpenAI クライアントもそのまま繋がる
- モデル名 `opus` / `sonnet` / `haiku` などでリクエストできる
- ツールは Ollama の JSON tool 定義のみ。Claude Code 組み込みツールはすべて無効にする
- Claude Code のデフォルトシステムプロンプトも無効にする。素の LLM として振る舞う

非目標

- 認証。`claude` CLI の認証をそのまま使う。oocla は
  API キーも OAuth も一切扱わないし、認証系のフラグも渡さない
- 会話の保存。毎ターン `--no-session-persistence` で実行し、ディスクには
  何も残さない (後述)
- 埋め込み (`/api/embeddings`)。`claude` CLI に相当機能がないため 501 を返す
- モデルの pull / push / delete。ローカルにモデル実体は存在しない

## 実測した CLI の挙動

`claude` 2.1.217 で確認した事実。実装の前提はここに書いてあるものだけ。

| 要件 | 手段 | 確認結果 |
| --- | --- | --- |
| 組み込みツールをすべて無効化 | `--tools ""` | init イベントの `tools` が `[]` になる |
| デフォルトプロンプト無効 | `--system-prompt <s>` | 置換される。空プロンプトでも `input_tokens` は 180 程度 |
| 設定ファイル無視 | `--setting-sources ""` | CLAUDE.md / settings.json を読まない |
| skill 無効 | `--disable-slash-commands` | init の `slash_commands` が `[]` |
| MCP 限定 | `--strict-mcp-config --mcp-config <json>` | 指定した MCP サーバのみ |
| 構造化出力 | `--json-schema <schema>` | Ollama の `format` に対応づける |
| ストリーム | `--output-format stream-json --verbose` | NDJSON でイベントが流れる |
| トークンの逐次出力 | `--include-partial-messages` | 部分チャンクが流れる |

`--bare` は使わない。認証が `ANTHROPIC_API_KEY` / apiKeyHelper に限定されてしまい、
OAuth 認証の環境で動かなくなるため。実測では `--bare` なしで
`apiKeySource: "none"` のまま正常応答した。

### 空のシステムプロンプト

Ollama のリクエストに system メッセージがない場合でも `--system-prompt ""` を必ず渡す。
空文字は「未指定」ではなく「空のシステムプロンプト」として扱われ、デフォルトが消える。

| 指定 | `input_tokens` |
| --- | --- |
| `--system-prompt ""` | 170 |
| `--system-prompt "You are a helpful assistant."` | 176 |

170 トークンが CLI のオーバーヘッドの下限。Claude Code 本来のエージェント用
システムプロンプト (数千トークン) は載っていない。

### 消しきれない自動挿入 (既知の制約)

`--system-prompt ""` でも以下がリクエストに残る。モデル自身に復唱させて確認した。

```
You are a Claude agent, built on Anthropic's Claude Agent SDK.

<system-reminder>
As you answer the user's questions, you can use the following context:
# userEmail
The user's email address is <ログイン中のメールアドレス>
# currentDate
Today's date is <日付>
...
</system-reminder>
```

つまり素の LLM ではなく「最小構成の Claude エージェント」になる。加えて
ログイン中のメールアドレスと日付が毎リクエストに載る。

試した手段と結果。

| 手段 | 結果 |
| --- | --- |
| `--system-prompt ""` | 固定プロンプトと system-reminder が残る |
| `--agents` + `--agent` | 固定プロンプトの後ろに連結されるだけ。残る |
| `--safe-mode` | `input_tokens` が同一 (184)。変化なし |
| `--exclude-dynamic-system-prompt-sections` | 同上。`--system-prompt` 併用時は無視される |
| `--bare` / `CLAUDE_CODE_SIMPLE=1` | 消える。ただし後述の代償あり |

`--bare` だけが実際に消せる。ただし認証が `ANTHROPIC_API_KEY` / apiKeyHelper に
限定され、OAuth ログインの環境では `Not logged in · Please run /login` で失敗する。

既定では使わない。`oocla serve --bare` で明示的に選んだときだけ有効になる。
このモードでも oocla は認証を扱わない。フラグを渡すだけで、認証の可否は CLI 側の
規則に従う。API キーを使っている環境なら、これで自動挿入は完全に消える。

### 作業ディレクトリ

CLI は cwd にある CLAUDE.md やプロジェクトメモリを読み込む。oocla を起動した
ディレクトリでそのまま CLI を実行するとそれらが会話に混入するため、
専用の空ディレクトリを作ってそこで実行する。

### stream-json 入力の挙動

`--input-format stream-json` に `{"type":"assistant",...}` の行を流し込むと、
履歴として効く。ただし `{"type":"user",...}` の行ごとに1ターン走り、
`result` イベントが行数分発生する。つまり履歴を毎回リプレイすると
API 呼び出しが履歴長に比例して増える。

## アーキテクチャ

```
oocla (単一バイナリ)
├── serve      : Ollama / OpenAI 互換 HTTP サーバ (:11434)
├── mcp-shim   : リクエストの tools を MCP ツールとして公開する子プロセス
└── version
```

### パッケージ構成

Ollama API と OpenAI API はスキーマに重なりがない。パスも `/api/*` と `/v1/*` で
完全に分かれる。そこで API ごとの実装を別パッケージに分け、共通の処理系を
その下に置く。二つの API 実装は互いに依存しない。

```
cmd/oocla         二つの実装を束ねる。/v1/ 配下 → openai、それ以外 → ollama
internal/ollama   Ollama API の実装。/api/* の入出力 JSON の型とパース・エンコードだけ
internal/openai   OpenAI API の実装。/v1/* の入出力 JSON の型とパース・エンコードだけ
internal/core     どちらの API にも依存しない中核。会話モデル (Message / Tool)、
                  モデルカタログ、履歴を1ターンにまとめる処理、stop 処理、Engine (実行)
internal/httpapi  小さな HTTP ルータ (405 と Allow を返す) と JSON ヘルパ
internal/bridge   core.Generator の実装。claude CLI を1回呼ぶ
```

不変条件 (いずれも `internal/core/boundary_test.go` で固定)。

- `internal/openai` と `internal/ollama` は相互に import しない
- この2つを import してよいのは `cmd/oocla` だけ。bridge / mcpshim /
  claudecli / httpapi は core の型だけに依存する
- どちらの API 実装も、自前の入出力の型を `core` の中間表現 (`core.ChatSpec`) に
  変換して `core.Engine` に渡す。一方の API の型を他方の変換経路に使うことはない
- エラーの形は API ごと。`/v1/*` は 404 / 405 も OpenAI の入れ子形式で返す
- `core.Message` の JSON タグは Ollama の入出力 JSON 形式そのもの。Ollama の形式が
  中間表現と 1:1 なので、Ollama 側はこれを直接使い、OpenAI 側は変換する。
  `core.Tool` は OpenAI 由来の形を Ollama がそのまま採用したので、両 API 共通

```
Ollama client
   │  POST /api/chat  (messages 全履歴 + tools)
   ▼
oocla serve
   │  履歴を1つの user ターンにまとめる (API 呼び出しは1回)
   ▼
claude -p --model haiku --tools "" --system-prompt ... --no-session-persistence
   │
   └── (tools 指定時) --strict-mcp-config --mcp-config → oocla mcp-shim
```

### セッションを持たない

毎ターン `--no-session-persistence` で実行する。`claude` CLI はディスクに
何も残さず、oocla も会話について何も覚えない。

どちらの API もステートレスで、クライアントは毎回全履歴を送ってくる。
oocla はそれを毎回1つの user ターンにまとめて送る。したがって入力トークンは
会話の長さに比例して増える。これは「何も残さない」ことの対価として受け入れる。

### ツール

Ollama のツール呼び出しはサーバ側では実行しない。サーバは `tool_calls` を返し、
クライアントが実行して結果を `role: "tool"` のメッセージで送り返す。

これを `claude` CLI で実現するため、リクエストの `tools` を MCP ツールとして
`oocla mcp-shim` に公開する。shim は oocla 自身のサブコマンドなので追加の
インストールは要らない。ツール定義は環境変数 `OOCLA_TOOLS` で渡す。

ツールは実行しない。モデルが `tool_use` ブロックを出した時点で、親プロセスが
ターンを打ち切って `tool_calls` として HTTP 応答を作る。`tool_use` は実行より前に
出力ストリームへ流れてくるので、shim の `tools/call` は通常呼ばれない。
ここまで到達した場合、shim は「ツールの結果は API クライアント側が返す」というエラーを返す。
偽の結果を返すとモデルがそれを前提に推論してしまうため。

打ち切りの副作用として `result` イベントが来ないので、ツール呼び出しで終わった
ターンは CLI の集計を受け取れない。代わりに実測で見つけた2つの情報源から
トークン数を組み立てる。

- 入力: assistant イベントが usage のスナップショットを持ち、入力側は
  リクエスト時点で確定しているので正確
- 出力: tools か構造化出力のあるターンでは `--include-partial-messages` を
  常時付ける。message の最後に来る `message_delta` イベントが確定の
  出力トークン数を持ち、しかも CLI がツールを実行に移るのは message が
  閉じた後なので、tool_use で即座に打ち切る代わりに `message_delta` まで
  読んでから打ち切れば、追加コストなしで確定値が取れる

所要時間だけは `result` にしかないため、打ち切られたターンでは報告できない。

CLI は子プロセス (MCP shim) を持つので、打ち切りはプロセスグループごと行う。
CLI だけを kill すると shim が出力パイプを開いたまま残る。

モデルから見えるツール名は `mcp__oocla__<name>` になる。応答の `tool_calls` では
プレフィックスを外して元の名前に戻すが、モデルが本文中でこの名前に言及することはある。

#### MCP がブロックされる環境でのフォールバック

managed settings (実測: `allowedMcpServers` の許可リスト) の環境では、
CLI が oocla の MCP サーバを起動しない。その場合 init イベントの
`mcp_servers` が空になるので、そこで検出してターンを即座に打ち切り、
別の手段でやり直す。モデルの応答前に打ち切るため、無駄になるのは
中断された呼び出し1回分だけ。

起動時にも一度検出して警告を出す。`claude mcp list` は許可されない名前の
サーバを一覧から消すことを実測で確認したので、scratch ディレクトリに
`.mcp.json` で shim を宣言して一覧に載るかを見る。モデルを呼ばないので
課金されない。managed-settings ファイルが読めれば、shim 名として使える
許可済みの名前を候補として警告に添える (ファイルを直接読む best-effort。
レジストリやサーバ配布のポリシーは見えない)。許可リストには `http://…` の
ような URL 形式のエントリも入り得るが、その名前の stdio サーバはポリシーが
許可していても CLI 自身が受け付けないことを実測したので、候補から除く。
リクエスト時の検出が本体で、起動時プローブはあくまで予告。

やり直しでは、ツール定義をシステムプロンプトに埋め込み、`--json-schema` で
`{content, tool_calls}` という形の応答を強制する。応答の `tool_calls` が空でなければ
通常のツール呼び出しとして返し、空なら `content` を本文として返す。
MCP 経由より頑健さは落ちるが、何も知らせずただの文章で返すよりはよい。

実測では、モデルの応答は2通りに分かれる。指示どおり `tool_calls` を埋めるものと、
プロンプトで示したツール名をそのまま `tool_use` ブロックで呼ぼうとするもの。
CLI は後者もイベントとして流してくるので、どちらも同じ要求として受け付ける。

スキーマの各フィールドには description を書く。これは実測で必要と判明した。
description なしでは、haiku がツールの結果を受けた次のターンで、`tool_calls` を
「過去の呼び出しの記録」と解釈して同じ呼び出しを埋める失敗が 3/3 で再現した
(thinking を出させると「結果を伝えよう」と正しく判断しており、壊れていたのは
応答形式への写しだけ)。「今から実行してほしい呼び出しであり、記録ではない」と
description に書いたら 3/3 で成功に変わった。

モデル別の成績 (`scripts/verify-prompt-tools.sh`、2026-07 実測):

| モデル | ツール呼び出し | ツールを使わない判断 | 結果を受けて回答 |
| --- | --- | --- | --- |
| opus | 5/5 | 2/2 | 3/3 |
| sonnet | 5/5 | 2/2 | 3/3 |
| haiku | 5/5 | 2/2 | 3/3 |
| fable | 5/5 | 2/2 | 3/3 |

`oocla serve --internal-prompt-tools` を付けると、MCP を使わず常にこの方式で動く。
フォールバックのデバッグ用。MCP が恒常的にブロックされる環境では、
検出のための中断呼び出し1回を毎リクエスト節約する効果もある。

制約: この応答形式が `--json-schema` を占有するため、tools と `format`
(構造化出力 / JSON モード) の併用はこの環境では成立しない。その場合は
理由を書いたエラーを返す。黙って tools を無視することはしない。

管理者が oocla 用の別名を許可リストに登録している環境向けに、
`oocla serve --internal-mcp-shim-name <name>` で shim の登録名を差し替えられる。
MCP 設定・`--allowedTools`・ツール名プレフィックスの除去がすべてこの名前に従う。
これは管理者が発行した名前に合わせるためのもので、他の許可済みサーバの名前を
名乗るためのものではない。正攻法は管理者に oocla を許可リストへ登録してもらうこと。

### モデル名

どちらの API でも同じ名前を受け付ける。

| クライアント側 | `--model` に渡す値 |
| --- | --- |
| `opus`, `opus:latest` | `opus` |
| `sonnet`, `haiku`, `fable` および `:latest` 付き | 同名 |
| `claude-` で始まる名前 | そのまま渡す |

`/api/tags` はこの一覧を返す。実体がないので `size` と `digest` は
モデル名から一意に生成する。

## ハーネス

`make check` が通ることを「グリーン」と定義する。

| コマンド | 内容 |
| --- | --- |
| `make fmt` | `gofmt -l` で差分ゼロを確認 |
| `make vet` | `go vet ./...` |
| `make test` | `go test -race ./...` |
| `make build` | `go build ./...` |
| `make check` | 上4つ |
| `make e2e` | サーバを起動して適合テストを流す。`claude` の認証済み環境が必要 |

標準ライブラリのみ。外部依存を追加しない。

## 構造化出力

`--json-schema` は CLI 内部で `StructuredOutput` という組み込みツールとして実装されている。
モデルは本文を出したあとこのツールを呼び、その引数が構造化された答えになる。
実測して判明した挙動なので、oocla 側で次のように扱う。

- スキーマ指定時は `StructuredOutput` の呼び出しを「答え」として扱い、`tool_calls` には出さない
- それまでの本文は前置きなので捨てる。Ollama は本文を返さないため
- ストリーミングでも前置きは流さず、確定した JSON を1チャンクで流す

`format: "json"` (スキーマなし) では `--json-schema` を使わない。制約する項目がないと
CLI が答えを `{"output": "..."}` というオブジェクトで包んでしまうため。
代わりにシステムプロンプトへ一文だけ指示を足す。oocla がプロンプトに文字列を
足すのはこの一箇所だけで、クライアントが明示的に JSON モードを要求したときに限る。

指示だけでは制約しきれないので、モデルは答えを ```json フェンスで包むことがある。
JSON モードではフェンスを取り除く。取り除いた結果が妥当な JSON のときだけ適用する。

## thinking

CLI 側で推論を止める手段はない。モデルは常に考える。したがって `think` が
制御するのは「クライアントに見せるかどうか」だけになる。これは Ollama の
仕様とも一致する (要求しない限り応答に thinking は載らない)。

| リクエスト | 挙動 |
| --- | --- |
| `think` なし / `false` | thinking を収集せず破棄する |
| `think: true` | `message.thinking` に載せる。effort はモデル既定 |
| `think: "low"` / `"medium"` / `"high"` | 載せたうえで `--effort <level>` を渡す |

`"xhigh"` と `"max"` も CLI が受け付けるので通す。Ollama の仕様にはないが、
Claude を前提にしたクライアントから届く可能性がある。

## options

Ollama の `options` はローカルの llama.cpp ランナーを設定するためのもので、
`claude` CLI には対応する設定がほとんどない。拒否せず、無視して警告ログに出す。
クライアントは中身を気にしていなくても options ブロックを送ってくるし、
Ollama 自身もモデルが実装していないオプションは無視するため。

唯一 `stop` だけは実装する。サンプリングを止めることはできないので、
出力を最初の stop シーケンスで切る。ストリーミングでは、チャンク境界を
またぐシーケンスを取りこぼさないよう、まだ途中かもしれない末尾を保留する。
偽陽性 (`EN` が `END` ではなく `ENOUGH` だった) の場合は保留分をそのまま出力する。

## OpenAI 互換層

Ollama は自前の API と並べて OpenAI 互換のエンドポイント群も提供しており、
OpenAI SDK で書かれたクライアントはそちらを使う。oocla も同じ経路を用意する。

| パス | 内容 |
| --- | --- |
| `POST /v1/chat/completions` | 本体。SSE ストリーミング対応 |
| `GET /v1/models` | モデル一覧 |
| `GET /v1/models/{model}` | 単体 |

`internal/openai` が入出力の型を `core.ChatSpec` に直接変換し、Ollama 側と
同じ `core.Engine` に流す。Ollama 側の型は経由しない。差分だけ挙げる。

- `stream` の既定値が false。Ollama 側は true
- ツール引数はオブジェクトではなく JSON 文字列
- ツール結果は `tool_call_id` で呼び出しを指す。ツール名は先行する
  `tool_calls` を参照して求める
- `content` は文字列と typed part の配列の両方を受ける。text 以外の part は 400
- `developer` ロールは system として扱う
- `reasoning_effort` を `think` と同じ effort レベルに対応づける
- エラーは `{"error":{"message":...,"type":...}}` の入れ子。Ollama の平坦な形と違う
- thinking は `reasoning` フィールドに載せる。OpenAI に該当フィールドがないため

## 後始末

- 作業ディレクトリは oocla が作った一時ディレクトリ。SIGINT / SIGTERM を受けて
  意図的に終了し、実行中のリクエストを待ってからこれを削除する
- `--no-session-persistence` を付けている限り CLI はトランスクリプトを
  書かないはずだが、保険として、作業ディレクトリから導出される
  `~/.claude/projects/<cwd をエンコードした名前>/` も終了時に削除する。
  ディレクトリ名に `oocla-cwd-` を含むときにしか導出しない
