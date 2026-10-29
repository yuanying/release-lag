# release-lag

Renovate の入ったリポジトリそれぞれについて、デフォルトブランチが最新のリリースタグから
どれだけ離れているか、何日リリースしていないかを集計し、HTML・JSON の API・Prometheus の
メトリクスで出すサーバー。

## 設計判断

実装に入る前に `docs/adr/` を読むこと。ここに書かれた決定を覆す実装をしないこと。覆す必要が
生じた場合は、先に ADR を追加または更新する。

- 起動のフラグ・設定ファイルの形・ポートとパス・Secret の場所・メトリクスの名前（ADR 0004）は
  fleet-infra の `apps/release-lag` との契約である。変えるときは ADR と README も直し、
  fleet-infra の側にも伝える
- アプリに認証のコードを入れない（ADR 0001）。gated の Ingress が担う
- 追跡の範囲と「最新のリリース」「離れ具合」の定義（ADR 0002）は、週次の自動リリース
  （`.github/scripts/plan-release.sh`）と同じ物差しに揃えてある

## 構成

| パッケージ | 役割 |
|---|---|
| `cmd/release-lag` | フラグ・設定の読み込み・起動 |
| `internal/config` | 設定ファイル |
| `internal/githubapp` | App の JWT と installation token |
| `internal/github` | GitHub の REST API のうち読むものだけ |
| `internal/github/githubtest` | テスト用の偽の GitHub（httptest） |
| `internal/lag` | 追跡の判定・タグの選び方・コミットの数え方・集計の保持 |
| `internal/server` | HTML・API・メトリクス・healthz |

## 開発の進め方

- 原則としてテスト駆動開発（TDD）。期待される入出力に基づきテストを先に書き、失敗を確認してから実装する
- テストの層:
  - `make test`: Go のツールチェーンだけで通る。GitHub は `githubtest` の偽物で置き換える。CI もこれ
  - `make live LIVE_CONFIG=hack/live.yaml`: 本物の GitHub に App で繋いで 1 回集計する。手で叩くだけで、`make test` や CI からは呼ばない
- GitHub の新しい API を使うときは、偽サーバーにも同じパスと形を足し、`make live` で本物と突き合わせる
- 公開リポジトリなので、テストのフィクスチャ・README・ADR に private リポジトリの実名を書かない（架空の名前を使う）
- 実装が終わったら `make fmt-check`・`make vet`・`make lint`・`make test` を通すこと

## ドキュメント

ドキュメントにソースコードを含めないこと。構造や挙動は文章と表で説明する。
