
# create mysql user and table
    GRANT INSERT,SELECT on kwh_period.* to kwh_agent@localhost IDENTIFIED by 'kwh_passwd';
    CREATE TABLE meter_log (period int(11) NOT NULL, kwh double NOT NULL, PRIMARY KEY (`period`)) ENGINE=InnoDB;

