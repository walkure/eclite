#!/usr/bin/env perl

# echonet.pl walkure at 3pf.jp
#

use strict;
use warnings;
use IO::Select;
use IO::Socket::INET;
use SKSock;
use YAML::Syck;
my $conf = LoadFile('./config.yaml');

my $isock = new IO::Socket::INET->new(
	LocalAddr => $conf->{tcp}{host},
	LocalPort => $conf->{tcp}{port},
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

my $client = undef;
while(1)
{
	foreach my $sock($s->can_read(undef))
	{
		if ($sock == $isock){
			$client->close if defined $client;
			$client = $sock->accept;
			if(defined $client){
				on_connected();
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
	
	my ($watt,$ap) = parse($data);
	if(defined $client){
		print $client "$watt\n";
		$client->close;
		$client = undef;
	}
}

sub on_connected
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
	my $num = substr($data,22,2) + 0;

	#check signature
	return if $ehd ne '1081';
	#check src object classes
	return if $seoj ne '0288';
	#check dst object classes
	return if $deoj ne '05FF';
	#check type is response
	return if $type ne '72';
	
	#load ECHONET lite packet chunk
	my $epc1 = substr($data,24,2);
	my $pdc1 = substr($data,26,2);
	my $edt1 = substr($data,28,8);

	my $epc2 = substr($data,36,2);
	my $pdc2 = substr($data,38,2);
	my $edt2 = substr($data,40,8);

	return if $epc1 ne 'E7';
	return if $epc2 ne 'E8';

	return unless ($pdc1 eq '04' and $pdc2 eq '04');
	
	print "EDT1: $edt1\n EDT2:$edt2\n";

	$edt1 =~s/([0-9a-fA-F]{2})/pack("H2",$1)/eg;
	$edt2 =~s/([0-9a-fA-F]{2})/pack("H2",$1)/eg;

	my ($watt) = unpack('N',$edt1);
	my ($ap,undef) = unpack('nn',$edt2);
	
	$ap /= 10;

	print "$watt W, $ap A\n";

	($watt,$ap);

}
