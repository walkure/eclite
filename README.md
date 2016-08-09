
## create mysql user and table
    GRANT INSERT,SELECT on kwh_period.* to kwh_agent@localhost IDENTIFIED by 'kwh_passwd';
    CREATE TABLE meter_log (period int(11) NOT NULL, kwh double NOT NULL, PRIMARY KEY (`period`)) ENGINE=InnoDB;

## add `zabbix_agent.conf`
	UserParameter=home.watt,nc -U /tmp/watt.sock
	UserParameter=home.ebill,cut -f2 /dev/shm/e-bill
	UserParameter=home.kwh,cut -f1 /dev/shm/e-bill

