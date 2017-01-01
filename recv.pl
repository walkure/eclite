#!/usr/local/bin/perl
# recv.pl walkure at 3pf.jp
# electric bill information receiver

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
my $dbh;

if(defined $ENV{REQUEST_METHOD}){
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

	connect_db();
	insert_data($epoch,$kwh);
	update_bill();
	print "OK";
	
}else{
	connect_db();
	update_bill();
}

$dbh->disconnect;

sub connect_db
{
	$dbh = DBI->connect('DBI:mysql:kwh_period;mysql_server_prepare=1','kwh_agent','kwh_passwd');
	unless(defined $dbh){
		print "Invalid\n";
		exit;
	}
}

sub insert_data
{
	my($epoch,$kwh) = @_;

	my $sth = $dbh->prepare('INSERT IGNORE INTO meter_log (period,kwh) VALUES (?,?)');
	$sth->bind_param(1,$epoch,DBI::SQL_INTEGER);
	$sth->bind_param(2,$kwh,DBI::SQL_DOUBLE);
	$sth->execute;
	$sth->finish;
}

sub update_bill
{
	my $now = localtime;

	my ($from,$to);

	if($now->mday >= 26){
		$now = Time::Piece->strptime($now->strftime('%Y-%m-26 +0900'),'%Y-%m-%d %z');
		my $next = $now->add_months(1);
		$from = $now->epoch;
		$to = $next->epoch;
	}else{
		$now = Time::Piece->strptime($now->strftime('%Y-%m-26 +0900'),'%Y-%m-%d %z');
		my $prev = $now->add_months(-1);
		$from = $prev->epoch;
		$to = $now->epoch;
	}

	my $sth = $dbh->prepare('SELECT kwh FROM meter_log WHERE period BETWEEN ? AND ? ORDER BY period LIMIT 1');
	$sth->execute($from,$to);
	my $r1 = $sth->fetchrow_arrayref;
	$sth->finish;

	$sth = $dbh->prepare('SELECT kwh,period FROM meter_log WHERE period BETWEEN ? AND ? ORDER BY period DESC LIMIT 2');
	$sth->execute($from,$to);
	my $r2 = $sth->fetchall_arrayref;
	$sth->finish;

	my $from_kwh  = ($r1->[0] + 0)*1000;
	my $to_kwh    = ($r2->[0][0] + 0)*1000;
	my $prev_kwh  = ($r2->[1][0] + 0)*1000;
	my $delta_sec = ($r2->[0][1] + 0) - ($r2->[1][1] +0);

	my $used_kwh = ($to_kwh - $from_kwh) / 1000;
	my $delta_wh = ($to_kwh - $prev_kwh) * (1800 / $delta_sec);
	my $yen = calculate($used_kwh);

	open(my $fh,'>/dev/shm/e-bill') or die "cannot open bill:$!\n";
	chmod 0666,'/dev/shm/e-bill';
	print $fh "$used_kwh\t$yen\t$delta_wh";
	close ($fh);
}

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

