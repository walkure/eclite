# eclite
PrometheusとECHONET Liteでスマートメータの記録を取る試み

※ Zabbix3.0の代わりにPrometheusを使う実装は、ブランチ`prometheus`に存在しています。

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

# 簡単な説明
http://www2.hatenadiary.jp/entry/2016/08/19/230106

# License
MIT
# Author
walkure at 3pf.jp
