#!/usr/bin/env perl

# echonet.pl walkure at 3pf.jp
# ECHONET Lite communicator

use strict;
use warnings;
use IO::Select;
use IO::Socket::INET;
use IO::Socket::UNIX;
require './SKSock.pm';
use YAML::Syck;
use HTTP::Daemon;
use HTTP::Status;
use HTTP::Response;

$|=1;

my $conf = LoadFile('./config.yaml');

my $sksock = SKSock->new(%{$conf->{bp35a1}});
$sksock->set_callback('erxudp',\&erxudp);
$sksock->set_callback('connected',\&on_connected);

my $htsock = HTTP::Daemon->new(LocalPort => $conf->{prometheus}{http}, ReuseAddr => 1 ) 
	or die "cannot start prometheus server at $conf->{prometheus}{http}:$!";

print "Listen on localhost:$conf->{prometheus}{http} \n";

$SIG{INT} = sub{
	print STDERR scalar localtime.":kill process...\n";
	$sksock->close;
	$htsock->close;
	die;
};

my $s = IO::Select->new();
$s->add($sksock);
$s->add($htsock);

$sksock->start;

print STDERR scalar localtime.":start process...\n";

my($mag,$kwh_mag,$period) = (0,0,0);
my($watt,$ap,$kwh) = ('NaN',undef,'NaN');
my $update = 0;

while(1)
{
	foreach my $sock($s->can_read(undef))
	{
		if($sock == $sksock){
			my $buf;
			my $len = $sock->sysread($buf,65535);
			if ($len)
			{
				$sock->parse_body($buf);
			}
		}elsif($sock == $htsock){
			my $client = $sock->accept;
			if(my $req = $client->get_request){
				if($req->method eq 'GET' and $req->uri->path eq '/metrics'){
					if($mag > 0 && $kwh_mag > 0){
						get_watt();
						$client->send_response(makeHttpResult());
					}else{
						print "not initialized\n";
						$client->send_error(RC_INTERNAL_SERVER_ERROR);
					}
				}else{
					$client->send_error(RC_NOT_FOUND);
				}
			}
			if($update > 0 and $update + 60 * $conf->{prometheus}{wdt} < time){
					printf STDERR "%s:WDT triggered. lastupdated:$update , now:%d \n",scalar localtime,scalar time;
					$sksock->terminate;
					#reset WDT
					$update = time;
			}

			$client->close;
			undef($client);
		}
	}
}

sub makeHttpResult
{
	my $res = HTTP::Response->new(RC_OK);

	my $labels = '';
	if(defined $conf->{prometheus}{labels}){
		my @label_list;
		foreach my $label(keys %{$conf->{prometheus}{labels}}){
			push(@label_list,"$label=\"$conf->{prometheus}{labels}{$label}\"");
		}
		$labels = '{'.join(',',@label_list).'}';
	}

	$res->add_content("# HELP kwh_total The Integrated Power Comsumption\n");
	$res->add_content("# TYPE kwh_total counter\n");
	$res->add_content(sprintf("kwh_total$labels %f \n" ,$kwh));

	$res->add_content("# HELP watt_now The Current Power Comsumption\n");
	$res->add_content("# TYPE watt_now gauge\n")	;
	$res->add_content(sprintf("watt_now$labels %d \n" ,$watt));

	$res;
}

sub erxudp
{
	my ($src,$dst,$srcport,$dstport,$srcmac,$secure,$length,$data) = @_;
	parse($data);

}

sub on_connected
{
	print STDERR scalar localtime.":send initial query\n";
	
	## ECHONET Lite ヘッダ
	# EHD1 0x10
	# EHD2 0x81
	# TID  0x01
	# EDATA

	## EDATA (ECHONET Lite データ)
	# SEOJ Class-group=0x05,Class=0xFF Instance=0x01 (コントローラ)
	# DEOJ Class-group=0x02,Class=0x88 Instance=0x01 (低圧スマート電力量メータ)
	# ESV  0x62 プロパティ読み出し
	# OPC  0x05 リクエスト数

	# EPC1 0xD3 積算消費電力量の係数(optional prop.)
	# EPC2 0xE1 積算消費電力量の単位(mandatory prop.)
	# EPC3 0xE0 積算電力量計測値(正方向) (mandatory)
	# EPC4 0xE7 瞬時電力計測値 (mandatory)
	# EPC5 0x82 APPENDIX Revision (mandatory)

	# PDCn 0x00 リクエストなのでEDTのサイズは0

	$sksock->send_udp("\x10\x81\x00\x01\x05\xFF\x01\x02\x88\x01\x62\x05\xD3\x00\xE1\x00\xE0\x00\xE7\x00\x82\x00");
}

sub get_watt
{
	print scalar localtime.":send query\n";
	$sksock->send_udp("\x10\x81\x00\x01\x05\xFF\x01\x02\x88\x01\x62\x02\xE0\x00\xE7\x00");
}

sub parse
{
	my $data = shift;

	#load ECHONET lite packet header
	my $ehd = substr($data,0,4);
	my $seoj = substr($data,8,4);
	my $deoj = substr($data,14,4);
	my $type = substr($data,20,2);

	#check signature(EHD1,2)
	return if $ehd ne '1081';
	#check src object classes(SEOJ)
	return if $seoj ne '0288';
	#check dst object classes(DEOJ)
	return if $deoj ne '05FF';

	# 52=Get_SNA(cannot retrive some props.)
	if($type eq '52' && $kwh_mag == 0)
	{
		# maybe 0xD3 not implemented.
		print STDERR scalar localtime.":re-send initial query\n";
		$sksock->send_udp("\x10\x81\x00\x01\x05\xFF\x01\x02\x88\x01\x62\x04\xE1\x00\xE0\x00\xE7\x00\x82\x00");
		$kwh_mag = 1;
		return;
	}

	#check type(ESV) 72=INF 73=Get_Res
	return unless $type eq '72' or $type eq '73';

	foreach my $packet (split_packet( substr ( $data,22) ))
	{
		my $type = unpack('C',$packet);
		my $edt = substr($packet,2);

		if($type == 0xd3){
			$kwh_mag = unpack('N',$edt);
			print STDERR scalar localtime.":kwh_mag:$kwh_mag\n";
		}elsif($type == 0xe0){
			die "init failed: kwh_mag:$kwh_mag mag:$mag\n" if($kwh_mag == 0 || $mag == 0);

			my $raw_kwh = unpack('N!',$edt);
			$kwh = $raw_kwh * $kwh_mag * $mag;
			printf("%f kwh (raw:%d kwh_mag:%d mag:%f)\n" ,$kwh,$raw_kwh,$kwh_mag,$mag);
		}elsif($type == 0xe1){
			my $mag_id = unpack('C',$edt);
			$mag = get_mag($mag_id);
			print STDERR scalar localtime.":mag:$mag\n";
		}elsif($type == 0xea){
			my($year,$month,$day,$hour,$min,$sec,$w) = unpack('nCCCCCN',$edt);
			my $step_kwh = $kwh_mag * $mag * $w;
			print "$year-$month-$day $hour:$min:$sec $step_kwh($w,$mag)"."kWh\n";
		}elsif($type == 0xe7){
			$watt = unpack('N!',$edt);
			print $watt."W\n";
			$update = time;
		}elsif($type == 0xe8){
			my($ap_r,$ap_t) = unpack('n!n!',$edt);
			
			$ap_r = $ap_r == 0x7ffe ? 0 : $ap_r;
			$ap_t = $ap_t == 0x7ffe ? 0 : $ap_t;

			$ap = ($ap_r+$ap_t) / 10;
			print $ap."A\n";
		}elsif($type == 0x82){
			my ($ver,$rev) = unpack('x2AC',$edt);
			print STDERR scalar localtime.":Supported ECHONET Lite APPENDIX Version:$ver,Revision:$rev\n";
		}
	}
}

sub split_packet
{
	my $data = shift;

	## ECHONET Lite のレスポンス
	# EPC 1byte データの種類
	# PDC 1byte データサイズ
	# EDT PDCに書かれたデータサイズ 返事

	$data =~s/([0-9a-fA-F]{2})/pack("H2",$1)/eg;
	my $count = unpack('c',$data);
	$data = substr($data,1);

	my @ret;

	for(my $i = 0 ; $i < $count ; $i++)
	{
		my ($type,$length) = unpack('CC',$data);
		$length += 2;
		my $packet = substr($data,0,$length);
		$data = substr($data,$length);

		push(@ret,$packet);
	}
	@ret;
}

sub get_mag
{
	my $id = shift;
	
	return 1      if $id == 0x00;
	return 0.1    if $id == 0x01;
	return 0.01   if $id == 0x02;
	return 0.001  if $id == 0x03;
	return 0.0001 if $id == 0x04;
	return 10     if $id == 0x0A;
	return 100    if $id == 0x0B;
	return 1000   if $id == 0x0C;
	return 10000  if $id == 0x0D;

	printf "unknown id=0x%X\n", $id;
	
	1;
}

