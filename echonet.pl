#!/usr/bin/env perl

# echonet.pl walkure at 3pf.jp
#

use strict;
use warnings;
use IO::Select;
use IO::Socket::INET;
use SKSock;
use YAML::Syck;
use Time::Local qw(timelocal);

my $conf = LoadFile('./config.yaml');

my $isock = new IO::Socket::INET->new(
	LocalAddr => $conf->{watt}{host},
	LocalPort => $conf->{watt}{port},
	Proto => 'tcp',
	Listen => 1,
	ReuseAddr => 1,

);
my $sksock = SKSock->connect(%{$conf->{bp35a1}});
$sksock->set_callback('erxudp',\&erxudp);
$sksock->set_callback('connected',\&on_connected);

$SIG{INT} = sub{
	print $sksock "SKTERM\r\n";
	die;
};

my $s = IO::Select->new();
$s->add($sksock);
$s->add($isock);

print "start process...\n";

my($mag,$kwh,$period,$watt,$ap) = (0,0,0,0,0);

my $client = undef;
while(1)
{
	foreach my $sock($s->can_read(undef))
	{
		if ($sock == $isock){
			$client->close if defined $client;
			$client = $sock->accept;
			if(defined $client){
				get_watt();
			}	
			next;
		}
		my $buf;
		my $len = $sock->sysread($buf,65535);
		if ($len)
		{
			$sock->parse_body($buf);
		}
		
	}
}

sub erxudp
{
	my ($src,$dst,$srcport,$dstport,$srcmac,$secure,$length,$data) = @_;
	
	parse($data);
	if(defined $client){
		print $client "$watt\n";
		$client->close;
		$client = undef;
	}
}

sub on_connected
{
	print "send initial query\n";
	$sksock->send_udp("\x10\x81\x00\x01\x05\xFF\x01\x02\x88\x01\x62\x02\xE1\x00\xEA\x00");

}

sub get_watt
{
	print "send query\n";
	$sksock->send_udp("\x10\x81\x00\x01\x05\xFF\x01\x02\x88\x01\x62\x02\xE7\x00\xE8\x00");

}

sub parse
{
	my $data = shift;

	#load ECHONET lite packet header
	my $ehd = substr($data,0,4);
	my $seoj = substr($data,8,4);
	my $deoj = substr($data,14,4);
	my $type = substr($data,20,2);

	#check signature
	return if $ehd ne '1081';
	#check src object classes
	return if $seoj ne '0288';
	#check dst object classes
	return if $deoj ne '05FF';
	#check type
	return unless $type eq '72' or $type eq '73';

	foreach my $packet (split_packet( substr ( $data,22) ))
	{
		my $type = unpack('C',$packet);
		my $edt = substr($packet,2);

		if($type == 0xe1){
			my $mag_id = unpack('C',$edt);
			$mag = get_mag($mag_id);
			print "mag:$mag\n";
		}elsif($type == 0xea){
			my($year,$month,$day,$hour,$min,$sec,$w) = unpack('nCCCCCN',$edt);
			$kwh = $mag * $w;
			print "$year-$month-$day $hour:$min:$sec $kwh($w,$mag)"."kWh\n";
			$year -= 1900 if $year < 1900;
			$month --;
			$period = timelocal($sec,$min,$hour,$day,$month,$year);
			update_period();
		}elsif($type == 0xe7){
			$watt = unpack('N',$edt);
			print $watt."W\n";
		}elsif($type == 0xe8){
			my($ap_r,$ap_t) = unpack('nn',$edt);
			
			$ap_r = $ap_r == 0x7ffe ? 0 : $ap_r;
			$ap_t = $ap_t == 0x7ffe ? 0 : $ap_t;

			$ap = ($ap_r+$ap_t) / 10;
			print $ap."A\n";
		}
	}
}

sub split_packet
{
	my $data = shift;

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
	return 0,1    if $id == 0x01;
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

sub update_period
{
	return if $kwh == 0;
	return if $period == 0;

	if(open(my $fh,'+< '.$conf->{period})){
		flock($fh,2);
		seek($fh,0,0);
		print $fh "$period:$kwh:".scalar localtime($period);
		truncate($fh,tell($fh));
		close $fh;
	}
}

