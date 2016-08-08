
# create mysql user and table
     GRANT all on kwh_period.* to kwh_agent@localhost identified by 'kwh_passwd';
    CREATE TABLE meter_log (period int(11) NOT NULL, kwh double NOT NULL, PRIMARY KEY (`period`)) ENGINE=InnoDB;

