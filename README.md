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

### dependencies
Perlのライブラリ系は基本的にはapt-getで入るはず。

### Docker

まずudevなどで、コンテナから `/dev/ttyUSB0` が見えるようにしておきます。


```
docker run --device=/dev/ttyUSB0:/dev/ttyWiSUN --mount type=bind,source=/home/walkure/eclite/conf,target=/conf -p 8881:8080 ghcr.io/walkure/eclite:latest
```

systemd経由で[podman](https://podman.io/)から起動するsystemd unit file例を添付しています。
なお、このファイルではSTDOUTログ出力を捨てています。


#### 実装を書き換えて動かす場合のメモ

- systemdのunit fileをoverrideする。
    - `[Service]`をoverrideするにはまず`ExecStart=`を書いてクリアしてから`ExecStart= /usr/bin...` のように書かないと置換でなく追加になる([参照](https://wiki.archlinux.org/title/Systemd#Examples))。 
- container imageをbuild
    - podman rootlessの場合buildしたimageは`$HOME`のstorageに入るので、systemdで動かすためにはrootのstorageに入るようrootでbuildするか[コピー](https://www.redhat.com/en/blog/podman-transfer-container-images-without-registry)する必要がある。
    - コマンド例
        - `podman build -t localhost/echonet .`
        - `podman image scp $USER@localhost::echonet`


# 簡単な説明
http://www2.hatenadiary.jp/entry/2016/08/19/230106

# License
MIT
# Author
walkure at 3pf.jp
