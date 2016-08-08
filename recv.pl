#!/usr/local/bin/perl

use strict;
use warnings;
use CGI;
use Digest::MD5 qw(md5_hex);
use YAML::Syck;
use DBI;
use URI::Escape;
use CGI::Carp qw(fatalsToBrowser);

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


my $dbh = DBI->connect('DBI:mysql:kwh_period','kwh_agent','kwh_passwd');
unless(defined $dbh){
	print "Invalid\n";
	exit;
}
my $sth = $dbh->prepare('INSERT IGNORE INTO meter_log (period,kwh) VALUES (?,?)');
$sth->bind_param(1,$epoch,DBI::SQL_INTEGER);
$sth->bind_param(2,$kwh,DBI::SQL_DOUBLE);
$sth->execute;

$sth->finish;
$dbh->disconnect;

print "OK\n";

