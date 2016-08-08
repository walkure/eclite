#!/usr/bin/env perl

use strict;
use warnings;
use Digest::MD5 qw(md5_hex);
use HTTP::Lite;
use URI::Escape;
use YAML::Syck;

my $conf = LoadFile('/home/walkure/eclite/config.yaml');

open(my $fh , $conf->{period}{shm}) or die "cannot open $conf->{period}{shm}:$!";
flock($fh,1);
my $data = <$fh>;
close($fh);

chomp $data;
my $digest = md5_hex($data.$conf->{period}{pkey});
$data = uri_escape($data);

my $req =  $conf->{period}{server}."?data=$data&key=$digest";
print "req:$req\n";

my $http = new HTTP::Lite;
my $code = $http->request($req);
my $body = $http->body();
print "[$body]\n";

1;


