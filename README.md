# eclite
PrometheusとECHONET Liteでスマートメータの記録を取る試み

※ PrometheusではなくZabbix 3.0を使う実装は、ブランチ`zabbix`に残しています。

低圧スマート電力量メータから、以下のデータを取得します。

* 瞬時電力計測値 
* 積算電力量計測値

瞬時と言いつつ答えが返ってくるまで数秒かかるので、聞かれた際には前回聞かれた際に取得した値を返しています。瞬時電流計測値は __データは0.1A単位で来るけど計測単位は1A__ という粗いデータだったので記録しないことにしました。

## 構成想定

* 家にRaspberry Piあたりのサーバがあり、Prometheus本体かPushProxが存在してる。
* 家のサーバにロームのWi-SUN通信モジュールBP35A1が刺さってる。

## 構築
配電会社からBルートパスワードをゲットしておきましょう。

`eclite`はGoで書かれたシングルバイナリで、実行時の外部依存はありません(CGO無効でビルドしています)。コンテナには詰めておらず、systemdから直接バイナリを起動する構成です。

### バイナリの入手

[Releases](https://github.com/walkure/eclite/releases)から対象アーキテクチャ(`linux-amd64`/`linux-arm64`/`linux-armv7`)のビルド済みバイナリを取得するのが手軽です。`vX.Y.Z`タグをpushするとGitHub Actionsが各アーキテクチャ向けにクロスコンパイルし、Releaseにバイナリと`checksums.txt`を添付します。

自分でビルドする場合はGoツールチェイン(go.modに記載のバージョン以上)だけあれば足ります。

```
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o eclite ./cmd/eclite
```

クロスコンパイルする場合は`GOOS`/`GOARCH`/`GOARM`を指定するだけです(例: Raspberry Pi 32bit向けなら`GOOS=linux GOARCH=arm GOARM=7`)。

### インストール

```
install -o root -g root -m 0755 eclite /usr/local/bin/eclite
install -d -o root -g dialout -m 0750 /etc/eclite
install -o root -g dialout -m 0640 config.yaml-dist /etc/eclite/config.yaml   # 中身を書き換える
install -m 0644 eclite.service /etc/systemd/system/eclite.service
systemctl daemon-reload
systemctl enable --now eclite
```

`eclite.service`は`DynamicUser=yes`で非rootユーザとして動作します。`/dev/ttyWiSUN`と`/etc/eclite/config.yaml`(Bルートのid/passを含む)はどちらも`dialout`グループ経由で読めるようにしています。動的に割り当てられるユーザには固定のグループが無いため、既存の`dialout`グループへの追加読み取り許可という形にしています(udevのグループ設定が異なる場合は`SupplementaryGroups=`と上記の`chgrp`先を合わせて調整してください)。単一ユーザーのホーム用途を前提にした簡略化なので、複数ユーザーがログインする環境では専用グループを切った方が安全です。

#### 実装を書き換えて動かす場合のメモ

- ビルドしたバイナリを`/usr/local/bin/eclite`に上書きコピーして`systemctl restart eclite`するだけです。コンテナ時代のように別ストレージへのコピーやイメージの参照先切り替えは不要になりました。
- unit fileそのものを一時的に変えたい場合は`systemctl edit eclite`でoverrideするのが安全です([参照](https://wiki.archlinux.org/title/Systemd#Examples))。


# 簡単な説明
http://www2.hatenadiary.jp/entry/2016/08/19/230106

# License
MIT
# Author
walkure at 3pf.jp
