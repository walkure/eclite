# SKSock.pm walkure at 3pf.jp
# Socket Module For SKSTACK(Rohm BP35A1)

package SKSock;
use strict;
use warnings;
use POSIX qw(:termios_h);

use base qw/IO::Termios/;

sub connect 
{
	my ($class,%args) = @_;
	
	my $dev = $args{device};

	my $self = $class->SUPER::open($dev);
	*$self->{recv_buf} = '';
	*$self->{sk_userid} = $args{id};
	*$self->{sk_passwd} = $args{pass};

	if(defined $args{speed}){
		print "Set speed $args{speed} bps\n";
		$self->setbaud($args{speed});
	}else{
		print "Set speed 115200 bps\n";
		$self->setbaud(115200);
	}
	
	#set termios attributes	
	my $pterm = POSIX::Termios->new;
	$pterm->getattr($self->fileno);

	my $lflags = $pterm->getlflag;
	my $oflags = $pterm->getoflag;
	
	$lflags &= ~(ICANON | ECHO | ECHOE);
	$oflags &= ~(OPOST);

	$pterm->setlflag($lflags);
	$pterm->setoflag($oflags);
	
	$pterm->setattr($self->fileno,TCSANOW);

	#start SKSTACK handshake
	$self->_sendCmd('SKVER');

	$self;
}

sub set_callback
{
	my($self,$name,$fn) = @_;
	
	if($name eq 'erxudp'){
		*$self->{sk_erxudp} = $fn;
	}elsif($name eq 'connected'){
		*$self->{sk_connected} = $fn;
	}
}

sub send_udp
{
	my($self,$data) = @_;
	return 0 unless $self->_mode() eq 'SK_CONNECTED';
	
	my $ipv6 = *$self->{sk_ipv6};
	my $len = sprintf('%04X',length $data);

	my $cmd = "SKSENDTO 1 $ipv6 0E1A 1 $len $data";
	$self->syswrite($cmd);
}

sub terminate
{
	my $self = shift;
	$self->_sendCmd('SKTERM');
}

sub _mode
{
	my ($self,$mode) = @_;
	*$self->{sk_mode} = $mode if(defined $mode and length $mode);
	*$self->{sk_mode};
}

sub _parse_line
{
	my($self,$line) = @_;

	my $mode = $self->_mode();
	return if length($line) == 0;
	print "<$mode>[$line]\n";

	if(substr($line,0,6) eq 'ERXUDP'){
		$self->_erxudp($line);
	}elsif(substr($line,0,5) eq 'EVENT'){
		$self->_event($line);
	}elsif(substr($line,0,4) eq 'FAIL'){
		if($mode eq 'SKTERM'){
			$self->_sendCmd('SKRESET');
		}else{
			die "command returns error";
		}
	}elsif($line eq 'OK'){
		$self->_ok($line);
	}elsif($line eq 'EPANDESC'){
		*$self->{sk_peerinfo} = {};
	}elsif($mode eq 'SKSCAN'){
		*$self->{sk_peerinfo}{channel} = $1 if($line =~/Channel:(\w+)/);
		*$self->{sk_peerinfo}{pan_id} = $1 if($line =~/Pan ID:(\w+)/);
		*$self->{sk_peerinfo}{addr} = $1 if($line =~/Addr:(\w+)/);
		*$self->{sk_peerinfo}{channel_page} = $1 if($line =~/Channel Page:(\w+)/);
		*$self->{sk_peerinfo}{lqi} = $1 if($line =~/LQI:(\w+)/);
		*$self->{sk_peerinfo}{pair_id} = $1	if($line =~/PairID:(\w+)/);
	}elsif($mode eq 'SKLL64'){
		if(length $line == 39){
			*$self->{sk_ipv6} = $line;
			$self->_sendCmd('SKJOIN',$line);
		}
	}elsif($mode eq 'ROPT'){
		if($line eq 'OK 00'){
			$self->_sendCmd('WOPT','01');
		}else{
			print "mode OK\n";
			$self->_mode('WOPT');
			$self->_parse_line('OK');
		}
	}elsif(length $line > 0){
		print "Unknown:{$line}\n";
	}
}


sub _sendCmd
{
	my($self,$mode,@args)= @_;

	my $cmd = join(' ',($mode,@args));
	$self->_mode($mode);

	print "send cmd[$cmd]\n";
	print $self "$cmd\r\n";
}



sub parse_body
{
	my($self,$body) = @_;
	
	$body =~ s/\x0D\x0A|\x0D|\x0A/\n/g;
	$body = *$self->{recv_buf} . $body if length *$self->{recv_buf};
	
	my $i;
	while (($i = index($body,"\n")) >= 0){ #index for the head of string is 0
		my $line = substr($body,0,$i);
		$body = substr($body,$i+1);
		$self->_parse_line($line);
	}
	*$self->{recv_buf} = $body;
}



sub _erxudp
{
	my ($self,$line) = @_;

	my (undef,$src,$dst,$srcport,$dstport,$srcmac,$secure,$length,$data) = split(/\s/,$line);

	if(defined *$self->{sk_erxudp} && $self->_mode() eq 'SK_CONNECTED'){
		*$self->{sk_erxudp}->($src,$dst,$srcport,$dstport,$srcmac,$secure,$length,$data);
	}else{
		print << "_DUMP_";
Sender: $src($srcmac)/$srcport
Dest  : $dst/$dstport
Secure: $secure
Length: $length
Data  : $data

_DUMP_
	}
}

sub _event
{
	my ($self,$line) = @_;
	my (undef,$id,$address) = split(/\s/,$line);

	if($id == 2){
		print "NA received\n";
	}elsif($id == 21){
		print "UDP sending finished\n";
	}elsif($id == 22){
		print "Active scan finished\n";
		unless(defined *$self->{sk_peerinfo}){
			$self->_sendCmd('SKSCAN','2','FFFFFFFF','6');
		}else{
			$self->_sendCmd('SKSREG S2',*$self->{sk_peerinfo}{channel});
		}
	}elsif($id == 24){
		$self->_sendCmd('SKTERM');
	}elsif($id == 25){
		print "PANA connection established\n";
		$self->_mode('SK_CONNECTED');
		*$self->{sk_connected}->($self) if defined *$self->{sk_connected};
	}elsif($id == 26){
		print "connection termination requested\n";
	}elsif($id == 27){
		print "PANA connection gracefully terminated\n";
	}elsif($id == 28){
		print "PANA connection termination timeout\n";
	}elsif($id == 29){
		print "PANA session timeout\n";
	}elsif($id == 32){
		print "sendlimit exceeded\n";
	}elsif($id == 33){
		print "sendlimit reset\n";
	}else{
		print ">$line\n";
	}
}

sub _ok
{
	my ($self,$line) = @_;

	my $mode = $self->_mode();
	if($mode eq 'SKVER'){
		$self->_sendCmd('SKINFO');
	}elsif($mode eq 'SKTERM'){
		$self->_sendCmd('SKRESET');
	}elsif($mode eq 'SKINFO'){
		$self->_sendCmd('SKRESET');
	}elsif($mode eq 'SKRESET'){
		$self->_sendCmd('SKSREG SFE 0');
	}elsif($mode eq 'SKSREG SFE 0'){
		$self->_sendCmd('ROPT');
	}elsif($mode eq 'WOPT'){
		$self->_sendCmd('SKSETPWD', 'C', *$self->{sk_passwd});
	}elsif($mode eq 'SKSETPWD'){
		$self->_sendCmd('SKSETRBID',*$self->{sk_userid});
	}elsif($mode eq 'SKSETRBID'){
		delete *$self->{sk_peerinfo};
		$self->_sendCmd('SKSCAN','2','FFFFFFFF','6');
	}elsif($mode eq 'SKSREG S2'){
		$self->_sendCmd('SKSREG S3',*$self->{sk_peerinfo}{pan_id});
	}elsif($mode eq 'SKSREG S3'){
		$self->_sendCmd('SKLL64',*$self->{sk_peerinfo}{addr});
	}
}
1;
