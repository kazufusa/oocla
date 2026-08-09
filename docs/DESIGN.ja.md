# oocla 設計

[English](DESIGN.md)

Ollama API と OpenAI API の両方に対応した HTTP サーバを立て、バックエンドとして
`claude` CLI を呼びます。Ollama クライアントには Ollama そのものに見え、
OpenAI クライアントには OpenAI 互換サーバに見えることを目標にします。

## 目標と非目標

目標

- Ollama クライアントが `http://localhost:11434` にそのまま繋がること
- OpenAI クライアントも `/v1/chat/completions` と `/v1/models` でそのまま繋がること
- モデル名 `opus` / `sonnet` / `haiku` などでリクエストできること
- ツールは Ollama の JSON tool 定義だけを使い、Claude Code の組み込みツールはすべて無効にすること
- Claude Code のデフォルトシステムプロンプトも無効にし、素の LLM として振る舞わせること

非目標

- 認証。`claude` CLI の認証をそのまま使います。oocla は API キーも OAuth も
  一切扱わず、認証系のフラグも渡しません
- 会話の保存。毎ターン `--no-session-persistence` で実行し、ディスクには
  何も残しません (後述)
- 埋め込み (`/api/embeddings`)。`claude` CLI に相当機能がないため 501 を返します
- モデルの pull / push / delete。ローカルにモデル実体がありません

## 実測した CLI の挙動

`claude` 2.1.217 で確認した事実です。実装の前提は、ここに書いたものだけです。

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
| エイリアスの解決先 | `--max-turns 0` | API を呼ばず、init イベントに解決済みモデル ID が載って終了する |

`--bare` は使いません。認証が `ANTHROPIC_API_KEY` / apiKeyHelper に限定され、
OAuth 認証の環境で動かなくなるためです。実測では `--bare` なしで
`apiKeySource: "none"` のまま正常に応答しました。

### 空のシステムプロンプト

Ollama のリクエストに system メッセージがない場合でも、`--system-prompt ""` を
必ず渡します。CLI は空文字を「未指定」ではなく「空のシステムプロンプト」として
扱うので、デフォルトが消えます。

| 指定 | `input_tokens` |
| --- | --- |
| `--system-prompt ""` | 170 |
| `--system-prompt "You are a helpful assistant."` | 176 |

170 トークンが CLI のオーバーヘッドの下限です。Claude Code 本来の
エージェント用システムプロンプト (数千トークン) は載っていません。

### 消しきれない自動挿入 (既知の制約)

`--system-prompt ""` でも以下がリクエストに残ります。モデル自身に復唱させて
確認しました。

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

つまり素の LLM ではなく「最小構成の Claude エージェント」になります。加えて、
ログイン中のメールアドレスと日付が毎リクエストに載ります。

試した手段と結果は次のとおりです。

| 手段 | 結果 |
| --- | --- |
| `--system-prompt ""` | 固定プロンプトと system-reminder が残る |
| `--agents` + `--agent` | 固定プロンプトの後ろに連結されるだけ。残る |
| `--safe-mode` | `input_tokens` が同一 (184)。変化なし |
| `--exclude-dynamic-system-prompt-sections` | 同上。`--system-prompt` 併用時は無視される |
| `--bare` / `CLAUDE_CODE_SIMPLE=1` | 消える。ただし後述の代償あり |

実際に消せるのは `--bare` だけです。ただし認証が `ANTHROPIC_API_KEY` /
apiKeyHelper に限定され、OAuth ログインの環境では
`Not logged in · Please run /login` で失敗します。

既定では使いません。`oocla serve --bare` で明示的に選んだときだけ有効になります。
このモードでも oocla は認証を扱いません。フラグを渡すだけで、認証の可否は
CLI 側の規則に従います。API キーを使っている環境なら、これで自動挿入は
完全に消えます。

### 作業ディレクトリ

CLI は cwd にある CLAUDE.md やプロジェクトメモリを読み込みます。oocla を
起動したディレクトリでそのまま CLI を実行するとそれらが会話に混入するため、
専用の空ディレクトリを作ってそこで実行します。

### stream-json 入力の挙動

`--input-format stream-json` に `{"type":"assistant",...}` の行を流し込むと、
履歴として効きます。ただし `{"type":"user",...}` の行ごとに 1 ターン走り、
`result` イベントが行数分発生します。つまり履歴を毎回リプレイすると、
API 呼び出しが履歴の長さに比例して増えます。

## アーキテクチャ

```
oocla (単一バイナリ)
├── serve      : Ollama / OpenAI 互換 HTTP サーバ (:11434)
├── mcp-shim   : リクエストの tools を MCP ツールとして公開する子プロセス
└── version
```

### パッケージ構成

Ollama API と OpenAI API はスキーマに重なりがなく、パスも `/api/*` と `/v1/*` で
完全に分かれます。そこで API ごとの実装を別パッケージに分け、共通の処理系を
その下に置きます。2 つの API 実装は互いに依存しません。

```
cmd/oocla         2 つの実装を束ねる。/v1/ 配下 → openai、それ以外 → ollama
internal/ollama   Ollama API の実装。/api/* の入出力 JSON の型とパース・エンコードだけ
internal/openai   OpenAI API の実装。/v1/* の入出力 JSON の型とパース・エンコードだけ
internal/core     どちらの API にも依存しない中核。会話モデル (Message / Tool)、
                  モデルカタログ、履歴を1ターンにまとめる処理、stop 処理、Engine (実行)
internal/httpapi  小さな HTTP ルータ (405 と Allow を返す) と JSON ヘルパ
internal/bridge   core.Generator の実装。claude CLI を1回呼ぶ
```

不変条件は次のとおりです (いずれも `internal/core/boundary_test.go` で固定)。

- `internal/openai` と `internal/ollama` は相互に import しません
- この 2 つを import してよいのは `cmd/oocla` だけです。bridge / mcpshim /
  claudecli / httpapi は core の型だけに依存します
- どちらの API 実装も、自前の入出力の型を `core` の中間表現 (`core.ChatSpec`) に
  変換して `core.Engine` に渡します。一方の API の型を他方の変換経路には使いません
- エラーの形は API ごとに決めます。`/v1/*` は 404 / 405 も OpenAI の
  入れ子形式で返します
- `core.Message` の JSON タグは Ollama の入出力 JSON 形式そのものです。
  Ollama の形式が中間表現と 1:1 なので、Ollama 側はこれを直接使い、
  OpenAI 側は変換します。`core.Tool` は OpenAI 由来の形を Ollama が
  そのまま採用したので、両 API で共通です

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

### セッションを持たない設計

毎ターン `--no-session-persistence` で実行します。`claude` CLI はディスクに
何も残さず、oocla も会話について何も覚えません。

どちらの API もステートレスで、クライアントは毎回全履歴を送ってきます。
oocla はそれを毎回 1 つの user ターンにまとめて送ります。したがって
入力トークンは会話の長さに比例して増えます。これは「何も残さない」ことの
対価として受け入れます。

### ツール

Ollama のツール呼び出しはサーバ側では実行しません。サーバは `tool_calls` を
返し、クライアントが実行して結果を `role: "tool"` のメッセージで送り返します。

これを `claude` CLI で実現するため、リクエストの `tools` を MCP ツールとして
`oocla mcp-shim` に公開します。shim は oocla 自身のサブコマンドなので、
追加のインストールは要りません。ツール定義は環境変数 `OOCLA_TOOLS` で渡します。

ツールは実行しません。モデルが `tool_use` ブロックを出した時点で、親プロセスが
ターンを打ち切って `tool_calls` として HTTP 応答を作ります。`tool_use` は
実行より前に出力ストリームへ流れてくるので、shim の `tools/call` は通常
呼ばれません。ここまで到達した場合、shim は「ツールの結果は API クライアント側が
返す」というエラーを返します。偽の結果を返すと、モデルがそれを前提に推論して
しまうためです。

打ち切りの副作用として `result` イベントが来ないので、ツール呼び出しで終わった
ターンでは CLI の集計を受け取れません。代わりに、実測で見つけた 2 つの情報源から
トークン数を組み立てます。入力トークンは、assistant イベントが持つ usage の
スナップショットから取ります。入力側はリクエスト時点で確定しているので正確です。
出力トークンのために、tools か構造化出力のあるターンでは
`--include-partial-messages` を常時付けます。message の最後に来る
`message_delta` イベントが、確定の出力トークン数を持ちます。CLI がツールの
実行に移るのは message が閉じた後なので、`tool_use` で即座に打ち切らず
`message_delta` まで読んでから打ち切れば、追加コストなしで確定値が取れます。

所要時間だけは `result` にしかないため、打ち切られたターンでは報告できません。

CLI は子プロセス (MCP shim) を持つので、打ち切りはプロセスグループごと
行います。CLI だけを kill すると、shim が出力パイプを開いたまま残ります。

モデルから見えるツール名は `mcp__oocla__<name>` になります。応答の
`tool_calls` ではプレフィックスを外して元の名前に戻しますが、モデルが本文中で
この名前に言及することはあります。

#### MCP がブロックされる環境でのフォールバック

managed settings (実測: `allowedMcpServers` の許可リスト) の環境では、CLI が
oocla の MCP サーバを起動しません。その場合は init イベントの `mcp_servers` が
空になるので、そこで検出してターンを即座に打ち切り、別の手段でやり直します。
モデルの応答前に打ち切るため、無駄になるのは中断された呼び出し 1 回だけです。

起動時にも一度検出して警告を出します。`claude mcp list` が許可されない名前の
サーバを一覧から消すことは実測で確認したので、scratch ディレクトリに
`.mcp.json` で shim を宣言し、一覧に載るかを見ます。モデルを呼ばないので
課金されません。managed-settings ファイルが読めれば、shim 名として使える
許可済みの名前を候補として警告に添えます (ファイルを直接読む best-effort で、
レジストリやサーバ配布のポリシーは見えません)。許可リストには `http://…` の
ような URL 形式のエントリも入り得ます。ただし、その名前の stdio サーバは
ポリシーが許可していても CLI 自身が受け付けないことを実測したので、候補から
除きます。リクエスト時の検出が本体で、起動時プローブはあくまで予告です。

やり直しでは、ツール定義をシステムプロンプトに埋め込み、`--json-schema` で
`{content, tool_calls}` という形の応答を強制します。応答の `tool_calls` が
空でなければ通常のツール呼び出しとして返し、空なら `content` を本文として
返します。MCP 経由より頑健さは落ちますが、何も知らせずただの文章で返すよりは
よいと判断しました。

実測では、モデルの応答は 2 通りに分かれます。指示どおり `tool_calls` を
埋めるものと、プロンプトで示したツール名をそのまま `tool_use` ブロックで
呼ぼうとするものです。CLI は後者もイベントとして流してくるので、どちらも
同じ要求として受け付けます。

スキーマの各フィールドには description を書きます。これは実測で必要と
判明しました。description なしでは、haiku が `tool_calls` を「過去の呼び出しの
記録」と解釈し、ツールの結果を受けた次のターンで同じ呼び出しを埋める失敗が
3/3 で再現しました (thinking を出させると「結果を伝えよう」と正しく判断して
おり、壊れていたのは応答形式への写しだけでした)。「今から実行してほしい
呼び出しであり、記録ではない」と description に書いたら、3/3 で成功に
変わりました。

モデル別の成績 (`scripts/verify-prompt-tools.sh`、2026-07 実測):

| モデル | ツール呼び出し | ツールを使わない判断 | 結果を受けて回答 |
| --- | --- | --- | --- |
| opus | 5/5 | 2/2 | 3/3 |
| sonnet | 5/5 | 2/2 | 3/3 |
| haiku | 5/5 | 2/2 | 3/3 |
| fable | 5/5 | 2/2 | 3/3 |

`oocla serve --internal-prompt-tools` を付けると、MCP を使わず常にこの方式で
動きます。フォールバックのデバッグ用です。MCP が恒常的にブロックされる
環境では、検出のための中断呼び出し 1 回を毎リクエスト節約する効果もあります。

制約: この応答形式が `--json-schema` を占有するため、この環境では tools と
`format` (構造化出力 / JSON モード) を併用できません。その場合は理由を書いた
エラーを返します。黙って tools を無視することはしません。

管理者が oocla 用の別名を許可リストに登録している環境向けに、
`oocla serve --internal-mcp-shim-name <name>` で shim の登録名を差し替えられます。
MCP 設定・`--allowedTools`・ツール名プレフィックスの除去が、すべてこの名前に
従います。これは管理者が発行した名前に合わせるためのもので、他の許可済み
サーバの名前を名乗るためのものではありません。正攻法は、管理者に oocla を
許可リストへ登録してもらうことです。

### モデル名

どちらの API でも同じ名前を受け付けます。

| クライアント側 | `--model` に渡す値 |
| --- | --- |
| `opus`, `opus:latest`, `opus:5` (解決済みバージョン) | `opus` |
| `sonnet`, `haiku`, `fable` およびタグ付き | 同名 |
| `claude-` で始まる名前 | そのまま渡す |

受け付けるタグは `latest` と、その時点で解決済みのバージョンの 2 つだけです。
それ以外は 404 を返します。特定リビジョンを提供する手段がないためです。

`/api/tags` はこの一覧を返します。実体がないので、`size` と `digest` は
モデル名から一意に生成します。

#### バージョンの解決 (起動時プローブ)

CLI はエイリアスを手元のテーブルで実モデル ID に解決します。`--max-turns 0` を
付けたゼロターン実行では、API を呼ばずに、init イベントで解決先の ID
(`claude-opus-5` など) を名乗って終了します (実測で 2 秒程度)。

起動時にエイリアスごとに 1 回この実行を行い、ID から読み取ったバージョンを
タグとしてカタログに反映します (`opus:5`、`haiku:4.5`)。`/api/show` の
`model_info` には `general.version` と解決先の ID (`claude.resolved_model`) が
入ります。

プローブはバックグラウンドで並列に走ります。完了までは該当エントリが
`:latest` のまま応答し、失敗したエントリはそのまま `:latest` で残ります。
プローブの結果でカタログが悪化することはありません。

### Ollama 互換の細部

クライアントの実装 (実例: strands-agents の Ollama プロバイダ) と Ollama の
実挙動に合わせて決めた点です。

- 終端の応答 (非ストリーミングの応答と、ストリームの最終行) では、
  統計フィールド 6 つ (`total_duration` `load_duration` `prompt_eval_count`
  `prompt_eval_duration` `eval_count` `eval_duration`) を、値が 0 でも必ず
  出します。実際の Ollama では常に値が入るため、クライアントは存在チェック
  なしで読みます。フィールドが欠けると、たとえば strands は
  `prompt_eval_count + eval_count` の加算で None 参照になって落ちます
- 途中のチャンクには逆に統計フィールドを載せません。Ollama のチャンクにも
  載っていません
- `load_duration` と `prompt_eval_duration` は、計測対象が存在しないため
  常に 0 です
- 空の `messages` (`/api/chat`) と空の `prompt` (`/api/generate`) は
  プリロード要求です。Ollama はこれを「モデルをメモリにロードしておけ」という
  指示と解釈し、生成せずに応答します。oocla にロードするものはありませんが、
  同じ形で応答します。モデルを呼ばず `done_reason: "load"` を返し、
  `keep_alive: 0` (数値でも `"0s"` のような文字列でも) なら `"unload"` を
  返します。エラーにすると、チャット開始前にウォームアップを送るクライアントが
  「モデルが使えない」と判断して止まります

## ハーネス

`make check` が通ることを「グリーン」と定義します。

| コマンド | 内容 |
| --- | --- |
| `make fmt` | `gofmt -l` で差分ゼロを確認 |
| `make vet` | `go vet ./...` |
| `make test` | `go test -race ./...` |
| `make build` | `go build ./...` |
| `make check` | 上4つ |
| `make e2e` | サーバを起動して適合テストを流す。`claude` の認証済み環境が必要 |

標準ライブラリだけで実装し、外部依存を追加しません。

## 構造化出力

`--json-schema` は、CLI 内部では `StructuredOutput` という組み込みツールとして
実装されています。モデルは本文を出したあとにこのツールを呼び、その引数が
構造化された答えになります。実測して判明した挙動なので、oocla 側では次のように
扱います。

- スキーマ指定時は `StructuredOutput` の呼び出しを「答え」として扱い、
  `tool_calls` には出しません
- それまでの本文は前置きなので捨てます。Ollama は本文を返さないためです
- ストリーミングでも前置きは流さず、確定した JSON を 1 チャンクで流します

`format: "json"` (スキーマなし) では `--json-schema` を使いません。制約する
項目がないと、CLI が答えを `{"output": "..."}` というオブジェクトで包んで
しまうためです。代わりに、システムプロンプトへ一文だけ指示を足します。
oocla がプロンプトに文字列を足すのはこの一箇所だけで、クライアントが明示的に
JSON モードを要求したときに限ります。

指示だけでは制約しきれないので、モデルは答えを ```json フェンスで包むことが
あります。JSON モードではフェンスを取り除きます。取り除いた結果が妥当な JSON の
ときだけ適用します。

## thinking

CLI 側で推論を止める手段はありません。モデルは常に考えます。したがって
`think` が制御するのは「クライアントに見せるかどうか」だけになります。これは
Ollama の仕様とも一致します (要求しない限り、応答に thinking は載りません)。

| リクエスト | 挙動 |
| --- | --- |
| `think` なし / `false` | thinking を収集せず破棄する |
| `think: true` | `message.thinking` に載せる。effort はモデル既定 |
| `think: "low"` / `"medium"` / `"high"` | 載せたうえで `--effort <level>` を渡す |

`"xhigh"` と `"max"` も CLI が受け付けるので通します。Ollama の仕様には
ありませんが、Claude を前提にしたクライアントから届く可能性があります。

## options

Ollama の `options` はローカルの llama.cpp ランナー向けの設定で、`claude` CLI
には対応する設定がほとんどありません。拒否せず、無視して警告ログに出します。
クライアントは中身を気にしなくても options ブロックを送ってきますし、
Ollama 自身も、モデルが実装していないオプションは無視するためです。

唯一 `stop` だけは実装します。サンプリングは止められないので、出力を最初の
stop シーケンスで切ります。ストリーミングでは、チャンク境界をまたぐ
シーケンスを取りこぼさないよう、まだ途中かもしれない末尾を保留します。
偽陽性だった場合 (`EN` が `END` ではなく `ENOUGH` だった場合) は、保留した
末尾をそのまま出力します。

## OpenAI 互換層

Ollama は自前の API と並べて、OpenAI 互換のエンドポイント群も用意しています。
OpenAI SDK で書かれたクライアントはそちらを使います。oocla も同じ経路を
用意します。

| パス | 内容 |
| --- | --- |
| `POST /v1/chat/completions` | 本体。SSE ストリーミング対応 |
| `GET /v1/models` | モデル一覧 |
| `GET /v1/models/{model}` | 単体 |

`internal/openai` が入出力の型を `core.ChatSpec` に直接変換し、Ollama 側と
同じ `core.Engine` に流します。Ollama 側の型は経由しません。差分だけ挙げます。

- `stream` の既定値が false です。Ollama 側は true です
- ツール引数はオブジェクトではなく JSON 文字列です
- ツール結果は `tool_call_id` で呼び出しを指します。ツール名は、先行する
  `tool_calls` を参照して求めます
- `content` は文字列と typed part の配列の両方を受けます。text 以外の part は
  400 にします
- `developer` ロールは system として扱います
- `reasoning_effort` は `think` と同じ effort レベルに対応づけます
- エラーは `{"error":{"message":...,"type":...}}` の入れ子です。Ollama の
  平坦な形と違います
- thinking は `reasoning` フィールドに載せます。OpenAI に該当フィールドが
  ないためです

## 後始末

作業ディレクトリは oocla が作った一時ディレクトリです。SIGINT / SIGTERM を
受けて意図的に終了するときは、実行中のリクエストを待ってからこれを削除します。

`--no-session-persistence` を付けている限り、CLI はトランスクリプトを
書かないはずです。保険として、作業ディレクトリから導出される
`~/.claude/projects/<cwd をエンコードした名前>/` も終了時に削除します。
ディレクトリ名に `oocla-cwd-` を含むときにしか導出しません。
