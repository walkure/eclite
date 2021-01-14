# eclite
Zabbix3.0とECHONET Liteでスマートメータの記録を取る試み

低圧スマート電力量メータから、以下のデータを取得します。

* 瞬時電力計測値 
* 定時積算電力量計測値

瞬時と言いつつ答えが返ってくるまで数秒かかるので、聞かれた際には前回聞かれた際に取得した値を返しています。また、いちおう瞬時電流計測値をとっていますが__データは0.1A単位で来るけど計測単位は1A__で、あんまりあてにならないので記録していません。。

また、定時積算電力量計測値は電力会社が記録しているのでこちらから聞かずとも30分に一回(毎正時と30分あたり)勝手にメータが発信しているので、それを受信しています(スクリプト起動時には計測値単位とともに聞きに行きます)。

## 構成想定

* 家にRaspberry Piあたりのサーバがある。
* 家のサーバにロームのWi-SUN通信モジュールBP35A1が刺さってる。
* ネットのどこかに監視サーバがある(さくらVPSで作りましたが)。
* 両方のサーバにzabbix_agentdが入っていて、zabbix_serverへの疎通はできている。

## 構築
電力会社からBルートパスワードをゲットしておきましょう。
### 家側
#### dependencies
Perlのライブラリ系は基本的にはapt-getで入るはず。

あと、zabbix_agentdをapt-getしたら古いのが降ってきたので自分でZabbix3.0のtarball取ってきてbuildしました。
PSK認証使うならconfigure時にTLSライブラリの指定が要ります。

#### データ送信設定
config.yamlに監視サーバのrecv.plを設定してください。
#### zabbix_agentd.confの設定
瞬時電力量計測値
`UserParameter=home.watt,nc -U /tmp/watt.sock`
### 監視サーバ側
#### データ受信設定
recv.plが何らかのWebサーバからCGIで叩けるようにします。
#### MySQLの設定
家から30分に一回、積算電力量計測値が送られてくるので、それをMySQLに記録します。

ユーザの作成
`GRANT INSERT,SELECT on kwh_period.* to kwh_agent@localhost IDENTIFIED by 'kwh_passwd';`

テーブルの作成
`CREATE TABLE meter_log (period int(11) NOT NULL, kwh double NOT NULL, PRIMARY KEY ('period')) ENGINE=InnoDB;`
#### zabbix_agentd.confの設定
今月の概算電気代
`UserParameter=home.ebill,cut -f2 /dev/shm/e-bill`

今月の消費電力量
`UserParameter=home.kwh,cut -f1 /dev/shm/e-bill`

30分間の消費電力量
`UserParameter=home.deltawh,cut -f3 /dev/shm/e-bill`

# 簡単な説明
http://www2.hatenadiary.jp/entry/2016/08/19/230106

# License
MIT
# Author
walkure at 3pf.jp
