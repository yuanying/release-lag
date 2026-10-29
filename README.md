# release-lag

Renovate の入ったリポジトリそれぞれについて、**デフォルトブランチが最新のリリースタグから
どれだけ離れているか**と、**何日リリースしていないか**を一覧にするサーバー。

Renovate のコミットだけなら週次の自動リリースがパッチ版を出すが、人のコミットが混ざると
手で版を切るまでリリースから離れたままになる。その溜まり具合を、表・JSON の API・
Prometheus のメトリクスで見せる。

- 追跡するのは、持ち主が設定の `owner` で、fork でもアーカイブでもなく、デフォルトブランチに
  Renovate の設定ファイルがあるリポジトリ。集計のたびに一覧を作り直すので、設定を足せば載り、
  消せば消える。除外の一覧も設定できる
- 最新のリリースは、semver のタグ（`v` の有無を問わない。プレリリースは除く）のうち一番大きいもの
- 離れ具合は、そのタグからデフォルトブランチまでのコミット数（マージコミットを除く）と、
  そのうち `renovate[bot]` 以外の数。GitHub の compare へのリンクを添える
- タグの無いリポジトリは「未リリース」として出す
- 既定の並びは、リリースしていない期間の長い順（未リリースが先頭）

定義の細部は [ADR 0002](docs/adr/0002-what-is-tracked-and-how-lag-is-measured.md) にある。

## 動かし方

`release-lag --config=/etc/release-lag/config.yaml --listen=:8080`

起動すると 1 回集計し、以降は設定の間隔で集計し直して、結果をメモリに持つ。
fraction クラスタでは fleet-infra の `apps/release-lag` で動き、gated の Ingress で
`https://release-lag.fraction.jp` に出る。アプリ自身は認証を持たない。

| フラグ | 既定 | 意味 |
|---|---|---|
| `--config` | `/etc/release-lag/config.yaml` | 設定ファイル |
| `--listen` | `:8080` | 待ち受けるアドレス |
| `--once` | | 1 回集計して JSON を出して終わる（`make live` が使う） |
| `--version` | | 版を出して終わる |

イメージは `ghcr.io/yuanying/release-lag:vX.Y.Z`。distroless の nonroot（65532）で、
読み取り専用のルートファイルシステムで動く。

## HTTP

ポートは 8080 の 1 つだけ。

| メソッドとパス | 内容 |
|---|---|
| `GET /` | 表の HTML。列の見出しを押すと並べ替える。「今すぐ集計」で集計し直す |
| `GET /api/repos` | 集計の結果の JSON。集計した時刻（`refreshedAt`）を含む |
| `POST /api/refresh` | その場で集計し直して、同じ JSON を返す。失敗なら 502 |
| `GET /healthz` | プロセスが動いていれば 200。GitHub に届くかとは無関係 |
| `GET /metrics` | Prometheus のメトリクス |

JSON のフィールドは [ADR 0004](docs/adr/0004-config-http-api-and-metrics-contract.md) の表が契約である。
リポジトリごとに名前・URL・デフォルトブランチ・private か・リリースの有無・最新のタグ・その日付・
経過日数・コミット数（全体と Renovate 以外）・compare の URL を持つ。未リリースのリポジトリでは
リリースに関する値が null になる。

## メトリクス

ラベル `repo` は owner なしのリポジトリ名。

| 名前 | 値 |
|---|---|
| `release_lag_days_since_release` | 最新のリリースタグの日付からの日数（タグの無いリポジトリには出ない） |
| `release_lag_commits_since_release` | タグからデフォルトブランチまでのコミット数（マージコミットを除く） |
| `release_lag_non_renovate_commits_since_release` | そのうち `renovate[bot]` 以外 |
| `release_lag_unreleased` | タグの無いリポジトリに 1、あるリポジトリに 0 |
| `release_lag_repository_info` | 追跡しているリポジトリごとに 1。ラベルにデフォルトブランチ・最新のタグ・private か |
| `release_lag_last_refresh_timestamp_seconds` | 最後の集計が終わった時刻 |
| `release_lag_last_refresh_success` | 最後の集計がすべて成功したら 1 |

## 設定

YAML の設定ファイルを起動時に読む。変えたらプロセス（Pod）を作り直す。知らないキーがあると起動しない。

| キー | 既定 | 意味 |
|---|---|---|
| `owner` | 必須 | 追跡する持ち主 |
| `interval` | `1h` | 集計の間隔（Go の duration。`30m` など） |
| `exclude` | 空 | 除外するリポジトリ名（owner なし）の一覧 |
| `github.appId` | 必須 | GitHub App の App ID |
| `github.installationId` | 必須 | App の installation ID |
| `github.privateKeyFile` | `/var/run/secrets/github-app/private-key.pem` | App の private key（PEM）のパス |
| `github.apiURL` | `https://api.github.com` | GitHub の REST API。テストで偽物を指すときに使う |

例は [`hack/live.example.yaml`](hack/live.example.yaml) にある。

## GitHub App を作る

release-lag は専用の GitHub App として GitHub を読む（[ADR 0003](docs/adr/0003-a-dedicated-github-app.md)）。

1. GitHub の Settings → Developer settings → GitHub Apps → **New GitHub App** を開く
2. 名前（例 `release-lag-yuanying`）と Homepage URL（このリポジトリの URL でよい）を入れる
3. **Webhook** の Active のチェックを外す（Webhook は使わない）
4. **Repository permissions** で次の 2 つだけを Read-only にする
   - **Contents: Read-only**（tree・タグ・compare を読む）
   - **Metadata: Read-only**（必須。リポジトリの一覧）
5. **Account permissions** は何も付けない
6. 「Where can this GitHub App be installed?」は **Only on this account** にして作る
7. 作った App の設定画面に出る **App ID** を控える
8. 同じ画面の **Private keys** で **Generate a private key** を押し、PEM をダウンロードする
9. 左の **Install App** から自分のアカウントにインストールし、**All repositories** を選ぶ
10. インストール後の設定画面の URL（`https://github.com/settings/installations/<数字>`）の
    最後の数字が **installation ID**

### 値の渡し方

| 値 | 渡し方 |
|---|---|
| App ID | 設定ファイルの `github.appId` |
| installation ID | 設定ファイルの `github.installationId` |
| private key | Secret `release-lag-github-app`（namespace `release-lag`）のキー `private-key.pem`。`/var/run/secrets/github-app/` に読み取り専用でマウントされる。手元では `github.privateKeyFile` にパスを書く |

fraction クラスタでの Secret の作り方（sops での暗号化）と apply の手順は fleet-infra の
`apps/release-lag/README.md` にある。

## 本物の GitHub で確かめる

`make test` と CI は外に繋がない。本物の GitHub に繋いで確かめるのは手で叩く `make live` だけである。

1. `hack/live.example.yaml` を `hack/live.yaml` に写し（git には入らない）、App ID・installation ID・
   ダウンロードした private key のパスを書く
2. `make live LIVE_CONFIG=hack/live.yaml` を実行する
3. 1 回集計した結果が `GET /api/repos` と同じ JSON で出る。`lastSuccess` が true で、
   Renovate の入ったリポジトリが並んでいれば App は正しく動いている。集計に失敗すると
   終了コードが 0 でなくなり、`lastError` に理由が出る

ブラウザで表も見たいときは、`go run ./cmd/release-lag --config=hack/live.yaml --listen=127.0.0.1:8080`
で起動して `http://127.0.0.1:8080/` を開く。

## 開発

入口は `Makefile`。`make help` でターゲットの一覧が出る。

| ターゲット | 内容 |
|---|---|
| `make test` | 単体テスト。GitHub は httptest の偽物で置き換える |
| `make fmt` / `make fmt-check` | gofmt |
| `make vet` / `make lint` | go vet / golangci-lint |
| `make vuln` | govulncheck |
| `make tidy` | go mod tidy |
| `make build` | `bin/release-lag` を作る |
| `make live` | 本物の GitHub で 1 回集計する（上を参照） |

golangci-lint と govulncheck は `go.mod` の `tool` で版を固定してあり、`go tool` で動く。

設計の判断は [`docs/adr/`](docs/adr/) にある。

## リリース

- `vX.Y.Z` のタグを push すると、テストを通してイメージ `ghcr.io/yuanying/release-lag:vX.Y.Z` を
  push し、GitHub Release を作る。バイナリのアーカイブは出さない
- 毎週月曜 09:00 JST に、前回のタグ以降のコミットが Renovate のものだけなら、次のパッチ版を自動で出す
- 依存の更新は Renovate（共通の preset `yuanying/renovate-config`）が行い、patch は自動でマージされる

## License

MIT
