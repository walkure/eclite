#!/usr/local/bin/perl

use strict;
use warnings;
use CGI;
use Digest::MD5 qw(md5_hex);
use YAML::Syck;
use DBI;
use URI::Escape;
use CGI::Carp qw(fatalsToBrowser);
use Time::Piece;

my $conf = LoadFile('/home/walkure/eclite/config.yaml');
my $q = new CGI;

print "Content-Type:text/plain\n\n";
my $data = $q->param('data');
my $key  = $q->param('key');

unless(defined $data or defined $key){
	print "Invalid\n";
	exit;
}

my $digest = md5_hex($data.$conf->{period}{pkey});

if($digest ne $key){
	print "Invalid\n";
	exit;
}

my @values = split(/:/,uri_unescape($data));
my $epoch = $values[0]+0;
my $kwh = $values[1]+0;


my $dbh = DBI->connect('DBI:mysql:kwh_period;mysql_server_prepare=1','kwh_agent','kwh_passwd');
unless(defined $dbh){
	print "Invalid\n";
	exit;
}
my $sth = $dbh->prepare('INSERT IGNORE INTO meter_log (period,kwh) VALUES (?,?)');
$sth->bind_param(1,$epoch,DBI::SQL_INTEGER);
$sth->bind_param(2,$kwh,DBI::SQL_DOUBLE);
$sth->execute;
$sth->finish;

my $now = localtime;
my $prev = Time::Piece->strptime($now->strftime('%Y-%m-15'), '%Y-%m-%d') - 86400 * 30;

my $from = Time::Piece->strptime($prev->strftime('%Y-%m-26'), '%Y-%m-%d');
my $to = Time::Piece->strptime($now->strftime('%Y-%m-26'), '%Y-%m-%d');

$sth = $dbh->prepare('SELECT kwh FROM meter_log WHERE period BETWEEN ? AND ? ORDER BY period LIMIT 1');
$sth->execute($from->epoch,$to->epoch);
my $r1 = $sth->fetchrow_arrayref;
$sth->finish;

$sth = $dbh->prepare('SELECT kwh FROM meter_log WHERE period BETWEEN ? AND ? ORDER BY period DESC LIMIT 1');
$sth->execute($from->epoch,$to->epoch);
my $r2 = $sth->fetchrow_arrayref;
$sth->finish;

$dbh->disconnect;

my $from_kwh = ($r1->[0] + 0)*1000;
my $to_kwh   = ($r2->[0] + 0)*1000;

my $diff_kwh = ($to_kwh - $from_kwh) / 1000;
my $yen = calculate($diff_kwh);

open(my $fh,'>/dev/shm/e-bill') or die "cannot open bill:$!\n";
print $fh "$diff_kwh\t$yen";
close ($fh);

print "OK\n";

sub calculate
{
	my $kwh = shift;

	# http://www.tepco.co.jp/ep/private/plan2/chargelist04.html#sec03
	my $yen = 842.4;
	if($kwh >= 300){
		$yen += 19.52 * 120 + 26.00 * 180 + 30.02 * ($kwh - 300);
	}elsif($kwh >= 120){
		$yen += 19.52 * 120 + 26.00 * ($kwh - 120);
	}else{
		$yen += 19.52 * $kwh;
	}

	$yen += 2.25 * $kwh;

	int($yen * 1.08);
}

