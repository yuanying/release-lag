# 0004. 設定ファイル・HTTP API・メトリクスの契約

- Date: 2026-10-29
- Status: Accepted

## Context

manifest は fleet-infra の `apps/release-lag` にあり、release-lag とは別の作業単位が書く。
起動のフラグ、設定ファイルの形、ポート、パス、Secret の場所、メトリクスの名前は両者の **契約**
になる。2026-10-29 に coordinator が決め、fleet-infra の作業単位はこれだけを前提にしている。

## Decision

### イメージと起動

- イメージは `ghcr.io/yuanying/release-lag:vX.Y.Z`。distroless の nonroot（uid/gid 65532）で動き、
  readOnlyRootFilesystem で動く（書き込み領域を要らない）
- 起動は `release-lag --config=/etc/release-lag/config.yaml`。`--config` の既定もこのパス
- ほかのフラグ: `--listen`（既定 `:8080`）
- 手で叩くためのフラグ（manifest は使わない）: `--once`（1 回集計して `GET /api/repos` と同じ JSON
  を標準出力に出して終わる。集計に失敗したら終了コード 1）、`--version`

### 設定ファイル

ConfigMap からマウントする YAML。起動時に読み、変えたら Pod を作り直す（fleet-infra は
configMapGenerator の hash で rollout させる）。

| キー | 意味 | 既定 |
|---|---|---|
| `owner` | 追跡する持ち主 | 必須 |
| `interval` | 集計の間隔（Go の duration。`0s` などの正でない値は誤り） | `1h` |
| `exclude` | 除外するリポジトリ名（owner なし）の一覧 | 空 |
| `github.appId` | GitHub App の App ID | 必須 |
| `github.installationId` | installation ID | 必須 |
| `github.privateKeyFile` | private key のパス | `/var/run/secrets/github-app/private-key.pem` |
| `github.apiURL` | GitHub の REST API（テスト用） | `https://api.github.com` |

知らないキーがあれば起動しない（綴りを誤ったキーが黙って既定に化けるのを防ぐ）。必須のキーが
無い、または `appId`・`installationId` が 0 のときも起動しない。

### Secret

`release-lag-github-app`（namespace `release-lag`）、キー `private-key.pem`。
`/var/run/secrets/github-app/` に読み取り専用でマウントする。

### HTTP

ポートは 1 つ: 8080（名前 `http`）。

| メソッドとパス | 応答 |
|---|---|
| `GET /` | HTML 1 枚。相対パスで API を読んで表を描く |
| `GET /api/repos` | 集計の結果（下の JSON）。200 |
| `POST /api/refresh` | その場で集計し直し、終わってから同じ JSON を返す。成功なら 200、失敗なら 502（本文は同じ形） |
| `GET /healthz` | プロセスが動いていれば 200 `ok`。GitHub への到達とは無関係（liveness / readiness 兼用） |
| `GET /metrics` | Prometheus の形式 |

パスに合わないメソッドには 405、どれにも合わないパスには 404 を返す。

`POST /api/refresh` の集計は要求から切り離して走らせる（上限 5 分）。クライアントが待ちきれずに
切っても、集計は途中で捨てられない。

### JSON の形

`GET /api/repos` と `POST /api/refresh` の本文。時刻は RFC 3339。

| フィールド | 型 | 意味 |
|---|---|---|
| `owner` | 文字列 | 設定の `owner` |
| `refreshedAt` | 時刻か null | `repositories` を集計した時刻。一覧が取れた集計がまだ無ければ null |
| `lastAttemptAt` | 時刻か null | 最後の集計が終わった時刻（成否を問わない） |
| `lastSuccess` | 真偽 | 最後の集計ですべてのリポジトリを測れたか |
| `lastError` | 文字列 | 最後の集計が失敗した理由。成功なら空 |
| `repositories` | 配列 | 追跡しているリポジトリ。既定の並び（ADR 0002）。空でも配列 |

`repositories` の要素:

| フィールド | 型 | 意味 |
|---|---|---|
| `name` | 文字列 | owner なしの名前 |
| `url` | 文字列 | GitHub のリポジトリの URL |
| `defaultBranch` | 文字列 | デフォルトブランチ |
| `private` | 真偽 | private か |
| `released` | 真偽 | リリースのタグがあるか。false なら「未リリース」 |
| `latestTag` | 文字列か null | 最新のリリースのタグ |
| `releasedAt` | 時刻か null | そのタグの日付 |
| `daysSinceRelease` | 整数か null | 応答した時刻までの経過日数（切り捨て） |
| `commitsSinceRelease` | 整数か null | タグからデフォルトブランチまでのコミット数（マージコミットを除く） |
| `nonRenovateCommitsSinceRelease` | 整数か null | そのうち `renovate[bot]` 以外 |
| `compareUrl` | 文字列か null | GitHub の compare の画面 |
| `error` | 文字列（無ければ省く） | 測れなかった理由 |

リリースに関する値は、未リリースのとき、またはその値を得る前に失敗したときに null になる。
fleet-infra はこの JSON に依存しない。なつみなどが読むときはこの表を契約とする。

### メトリクス

ラベル `repo` は owner なしの名前。すべて gauge。

| 名前 | ラベル | 値 |
|---|---|---|
| `release_lag_days_since_release` | `repo` | 最新のリリースのタグの日付からの日数（小数。読まれた時刻で計算する）。タグの無いリポジトリには出さない |
| `release_lag_commits_since_release` | `repo` | タグからデフォルトブランチまでのコミット数（マージコミットを除く）。タグの無いリポジトリには出さない |
| `release_lag_non_renovate_commits_since_release` | `repo` | そのうち `renovate[bot]` 以外 |
| `release_lag_unreleased` | `repo` | タグの無いリポジトリに 1、あるリポジトリに 0。タグを読む前に失敗したリポジトリには出さない |
| `release_lag_repository_info` | `repo`・`default_branch`・`latest_tag`（無ければ空）・`private`（`true`/`false`） | 追跡しているリポジトリごとに 1 |
| `release_lag_last_refresh_timestamp_seconds` | なし | 最後の集計が終わった時刻（unix 秒、成否を問わない）。まだなら 0 |
| `release_lag_last_refresh_success` | なし | 最後の集計ですべてのリポジトリを測れたら 1、それ以外は 0 |

`release_lag_repository_info` は release-lag の作業単位が足した。ほかの名前は coordinator が
決めたもので、変えない。ほかに Go のランタイムとプロセスの標準のメトリクスを出す。

## Consequences

- 契約を変えるときは、この ADR・README・fleet-infra の manifest を一緒に直す
- `days_since_release` を読まれた時刻で計算するので、集計が止まっても日数は増え続ける。
  集計が止まったことは `last_refresh_timestamp_seconds` で分かる
