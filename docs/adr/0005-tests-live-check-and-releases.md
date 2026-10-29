# 0005. テスト、本物の GitHub での確認、リリース

- Date: 2026-10-29
- Status: Accepted

## Context

集計の正しさは GitHub API の応答に懸かっているが、CI から本物の GitHub に繋ぐと、資格を
CI に置くことになり、結果も GitHub の状態で揺れる。一方で、本物に繋いだ確認が一度も無いと、
API の形の思い違いに気づけない。

## Decision

### テスト

- GitHub API は `httptest` の偽サーバー（`internal/github/githubtest`）で置き換える。偽サーバーは
  リポジトリ・ファイル・タグ・compare の結果をデータとして受け取り、本物と同じパスと形で答え、
  `Link` ヘッダで頁を分ける。installation token の発行にも答える
- 確かめること: 設定の無いリポジトリは載らない、設定の各場所、fork・アーカイブ・他の owner・
  除外設定、空のリポジトリ、一覧の作り直し、タグ無し（プレリリースだけを含む）、semver の選び方、
  注釈付きと軽量のタグの日付、Renovate 以外のコミット数、マージコミットを数えない、頁を跨ぐ一覧、
  一部のリポジトリの失敗、一覧の失敗で前回の結果を残すこと、HTTP の各パスと JSON の形、
  メトリクス、設定の既定と誤り、App の JWT と token の更新
- `make test` は Go のツールチェーンだけで通り、外に繋がない。CI も同じ

### 本物の GitHub での確認

- `make live LIVE_CONFIG=<設定ファイル>` を手で叩く。`release-lag --once` で 1 回集計し、
  `GET /api/repos` と同じ JSON を出す。集計に失敗すれば終了コードが 0 でない
- 使うのは本番と同じ **App の設定ファイル**（App ID・installation ID・private key のパス）。
  `GITHUB_TOKEN` の口は作らない。`/installation/repositories` は installation token でしか
  呼べず、別の経路を足すと本番と違うコードを確かめることになるため
- `make test` からは呼ばない。設定の例は `hack/live.example.yaml` に置き、手元の `hack/live.yaml` は `.gitignore` に入れる

### リリース

- 出すのはコンテナイメージ `ghcr.io/yuanying/release-lag:vX.Y.Z` だけで、バイナリのアーカイブは
  出さない。`make dist` も置かない
- `vX.Y.Z` のタグの push でテストとイメージのビルドを行い、GitHub Release を作る
- 週次（毎週月曜 09:00 JST）に、前回のタグ以降のコミットが Renovate のものだけならパッチ版を出す
  （repo-bootstrap の `plan-release.sh` と `weekly-release.yml`）
- イメージは単一アーキテクチャ（linux/amd64）。repo-bootstrap の雛形に揃える
- Go のツールチェーンは `go.mod` の `toolchain` で修正版を指す。言語の版（`go`）とは分けて、
  標準ライブラリの脆弱性の修正だけを取り込めるようにする

## Consequences

- 偽サーバーが本物と違う答え方をしていると、テストは通っても本番で外れる。`make live` で本物と
  突き合わせて、ずれが見つかれば偽サーバーを直す
- 最初の `v0.1.0` は手でタグを打つ。週次リリースは前回のタグを起点にするため
