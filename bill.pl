#!/usr/bin/env perl
use strict;
use warnings;

use DBI;
use Time::Piece;
use Data::Dumper;

my $dbh = DBI->connect('DBI:mysql:kwh_period','kwh_agent','kwh_passwd')
	or die "cannot connect DBI:$DBI::errstr\n";

my $now = localtime;
my $prev = Time::Piece->strptime($now->strftime('%Y-%m-15'), '%Y-%m-%d') - 86400 * 30;

my $from = Time::Piece->strptime($prev->strftime('%Y-%m-26'), '%Y-%m-%d');
my $to = Time::Piece->strptime($now->strftime('%Y-%m-26'), '%Y-%m-%d');

my $sth = $dbh->prepare('select kwh from meter_log where period between ? and ? order by period limit 1');
$sth->execute($from->epoch,$to->epoch);
my $r1 = $sth->fetchrow_arrayref;
$sth->finish;

$sth = $dbh->prepare('select kwh from meter_log where period between ? and ? order by period desc limit 1');
$sth->execute($from->epoch,$to->epoch);
my $r2 = $sth->fetchrow_arrayref;
$sth->finish;

$dbh->disconnect;

my $from_kwh = ($r1->[0] + 0)*1000;
my $to_kwh   = ($r2->[0] + 0)*1000;

my $diff_kwh = ($to_kwh - $from_kwh) / 1000;
my $yen = calculate($diff_kwh);

print "$yen\n";


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

