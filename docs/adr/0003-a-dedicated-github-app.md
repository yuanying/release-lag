# 0003. GitHub の身元は専用の GitHub App

- Date: 2026-10-29
- Status: Accepted

## Context

private リポジトリも含めて、yuanying のリポジトリの一覧、tree、タグ、compare を読む必要がある。
個人のアクセストークンを Secret に置くと、権限が広く、期限の管理も人の手に残る。

## Decision

- 専用の GitHub App を作る。権限は Repository permissions の **Contents: Read-only** と
  **Metadata: Read-only** だけ。Account permissions は要らない。Webhook は使わない。
  インストール先は yuanying の **All repositories**
- アプリは App の private key（PEM。GitHub が渡す PKCS#1 のほか PKCS#8 も読める）で RS256 の
  JWT に署名し、installation token と引き換える。token は 1 時間で失効するので、失効の 5 分前を
  過ぎたら次の token を発行し直す。JWT は 9 分で失効させ、時計のずれに備えて発行時刻を 1 分
  前にずらす
- JWT の署名は標準ライブラリで書く。そのためだけに JWT のライブラリを足さない
- App ID・installation ID・private key のファイルの場所は設定ファイルの `github` で渡す
  （ADR 0004）。private key は Secret `release-lag-github-app` のキー `private-key.pem` を
  `/var/run/secrets/github-app/` に読み取り専用でマウントする。起動時に 1 回読む
- 追跡するリポジトリの一覧は、installation から見えるリポジトリ（`/installation/repositories`）
  から作る

## Consequences

- 権限は読み取りだけで、リポジトリに書き込めない
- private key を差し替えたら Pod を作り直す
- installation token でしか `/installation/repositories` を呼べないので、個人のアクセストークン
  では同じ経路で動かせない。本物の GitHub での確認（ADR 0005）も App の設定で行う
- App の作成とインストール、private key の sops での暗号化はユーザーが行う
