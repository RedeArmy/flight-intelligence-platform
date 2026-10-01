-- Sets the role passwords from the Docker secrets mounted at /run/secrets. Runs once, when the data volume is first
-- initialised, right after deployments/postgres/roles.sql (10-roles.sql). Backticks are psql shell expansion: the values
-- never appear on a command line and are never written to the image or to this repository.
\set migrator_pw `cat /run/secrets/postgres_migrator_password`
\set app_pw `cat /run/secrets/postgres_password`
\set admin_pw `cat /run/secrets/postgres_admin_password`
\set readonly_pw `cat /run/secrets/postgres_readonly_password`

ALTER ROLE fip_migrator PASSWORD :'migrator_pw';
ALTER ROLE fip_app PASSWORD :'app_pw';
ALTER ROLE fip_admin PASSWORD :'admin_pw';
ALTER ROLE fip_readonly PASSWORD :'readonly_pw';
