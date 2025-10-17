<!--
# Copyright 2021 - 2025 Crunchy Data Solutions, Inc.
#
# SPDX-License-Identifier: Apache-2.0
-->

# PostgreSQL Parameters Configuration

This document describes how PostgreSQL server parameters are configured and managed by PGO (the Postgres Operator).

## Overview

PostgreSQL parameters control the behavior of the database server. They can affect performance, resource usage, security, logging, replication, and many other aspects of database operation.

Parameters are configured through the PostgresCluster custom resource and are ultimately written to `postgresql.conf` and managed by Patroni.

## Configuration Methods

### Via PostgresCluster Spec

The primary method for configuring PostgreSQL parameters is through the PostgresCluster specification:

```yaml
apiVersion: postgres-operator.crunchydata.com/v1beta1
kind: PostgresCluster
metadata:
  name: my-cluster
spec:
  patroni:
    dynamicConfiguration:
      postgresql:
        parameters:
          max_connections: "100"
          shared_buffers: "256MB"
          effective_cache_size: "1GB"
          maintenance_work_mem: "64MB"
          checkpoint_completion_target: "0.9"
          wal_buffers: "16MB"
          default_statistics_target: "100"
          random_page_cost: "1.1"
          effective_io_concurrency: "200"
          work_mem: "4MB"
          min_wal_size: "1GB"
          max_wal_size: "4GB"
```

### Parameter Categories

PostgreSQL parameters fall into several categories based on when changes take effect:

1. **Requires Restart** - Changes require a PostgreSQL restart
   - `shared_buffers`, `max_connections`, `wal_level`, `max_wal_senders`, etc.

2. **Requires Reload** - Changes take effect with `pg_ctl reload` or `SELECT pg_reload_conf()`
   - `log_statement`, `ssl`, `archive_command`, `work_mem`, etc.

3. **Immediate** - Changes take effect immediately for new sessions
   - `timezone`, `search_path`, `statement_timeout`, etc.

When parameters requiring restart are changed in the PostgresCluster spec, PGO will perform a rolling restart of PostgreSQL instances to apply the changes.

## Mandatory Parameters

The operator sets certain parameters that **MUST NOT** be overridden by users. These are defined in `parameters.go` and are essential for proper operation:

### Security Parameters

```
ssl = on
ssl_cert_file = /pgconf/tls/tls.crt
ssl_key_file = /pgconf/tls/tls.key
ssl_ca_file = /pgconf/tls/ca.crt
```

All connections to PostgreSQL are encrypted with TLS. The operator manages certificates and their paths.

### Replication Parameters

```
wal_level = logical
```

Enables logical replication in addition to streaming replication and WAL archiving. This allows for flexible replication topologies and logical decoding.

### Connection Parameters

```
unix_socket_directories = /tmp/postgres
```

Specifies the directory for UNIX domain sockets. This is used for local connections within the pod.

### Logging Parameters

```
log_file_mode = 0660
```

Sets file permissions on log files to allow both the postgres user and the pod's fsGroup to read logs. This enables log collection by sidecars and monitoring tools.

## Default Parameters

The operator provides sensible defaults for certain parameters. Users can override these in the PostgresCluster spec:

```
jit = off
```

Just-in-Time (JIT) compilation is disabled by default as it can degrade performance for some workloads. Enable it for analytical queries that benefit from JIT.

```
password_encryption = scram-sha-256
```

Uses the more secure SCRAM-SHA-256 password authentication method instead of MD5. Supported on PostgreSQL 10+.

```
log_directory = /pgdata/logs/postgres
```

Logs are written outside the PostgreSQL data directory to:
- Preserve logs during replica creation and major upgrades
- Reduce backup size
- Simplify log collection

## Parameter Precedence

PostgreSQL parameters can come from multiple sources. The precedence order (highest to lowest) is:

1. **Mandatory parameters** - Set by the operator, cannot be overridden
2. **User-specified parameters** - Set in `spec.patroni.dynamicConfiguration.postgresql.parameters`
3. **Default parameters** - Set by the operator, can be overridden
4. **PostgreSQL built-in defaults** - PostgreSQL's compiled-in defaults

## Common Parameter Recommendations

### Memory Settings

```yaml
# Rule of thumb: 25% of system RAM
shared_buffers: "2GB"

# Rule of thumb: 50-75% of system RAM
effective_cache_size: "6GB"

# Rule of thumb: 5% of shared_buffers, up to 512MB
work_mem: "100MB"

# Rule of thumb: 5% of RAM, up to 2GB
maintenance_work_mem: "512MB"
```

### WAL and Checkpointing

```yaml
# Minimum size to shrink WAL disk usage
min_wal_size: "1GB"

# Maximum size before checkpoint is forced
max_wal_size: "4GB"

# Spread checkpoint writes over this fraction of checkpoint interval
checkpoint_completion_target: "0.9"

# WAL buffers (auto-tuned to 3% of shared_buffers up to 16MB by default)
wal_buffers: "-1"  # Auto
```

### Query Planner

```yaml
# Set to 1.1 for SSD, 4.0 for HDD
random_page_cost: "1.1"

# Number of concurrent I/O operations for SSDs
effective_io_concurrency: "200"

# Higher values cause more accurate statistics but slower ANALYZE
default_statistics_target: "100"
```

### Connection and Resource Limits

```yaml
# Maximum number of connections (reserve some for superuser)
max_connections: "100"

# Maximum number of WAL sender processes for replication
max_wal_senders: "10"

# Maximum number of replication slots
max_replication_slots: "10"

# Maximum number of prepared transactions (0 disables two-phase commit)
max_prepared_transactions: "0"
```

### Logging Configuration

```yaml
# When to log statements: none, ddl, mod, all
log_statement: "none"

# Log slow queries (in milliseconds, 0 disables)
log_min_duration_statement: "1000"

# Line prefix format
log_line_prefix: "%t [%p]: [%l-1] user=%u,db=%d,app=%a,client=%h "

# Log connections and disconnections
log_connections: "on"
log_disconnections: "on"

# Log lock waits longer than this (in milliseconds)
log_lock_waits: "on"
deadlock_timeout: "1000"

# Log checkpoints
log_checkpoints: "on"
```

### Autovacuum Tuning

```yaml
# Enable autovacuum (should always be on)
autovacuum: "on"

# Autovacuum runs when this fraction of table is modified
autovacuum_vacuum_scale_factor: "0.1"
autovacuum_analyze_scale_factor: "0.05"

# Maximum number of autovacuum workers
autovacuum_max_workers: "3"

# Cost-based delay to throttle autovacuum
autovacuum_vacuum_cost_delay: "2ms"
autovacuum_vacuum_cost_limit: "200"
```

## Advanced Configuration

### Archive Command

The operator manages `archive_command` for pgBackRest integration. Do not override this parameter.

### Recovery Configuration

For standby clusters, the operator configures `recovery_target_timeline`, `restore_command`, and related parameters automatically. See the [standby cluster documentation](../../testing/kuttl/e2e/streaming-standby/README.md).

### Huge Pages

The operator can configure transparent huge pages based on system capabilities. See `huge_pages.go` for implementation details.

### Shared Preload Libraries

To load extensions at server start:

```yaml
spec:
  patroni:
    dynamicConfiguration:
      postgresql:
        parameters:
          shared_preload_libraries: "pg_stat_statements,auto_explain"
```

Note: Changing `shared_preload_libraries` requires a PostgreSQL restart.

## Parameter Validation

PostgreSQL validates parameters when they are applied:
- Invalid parameter names cause warnings in logs but don't prevent startup
- Invalid values for valid parameters prevent startup
- Some parameters have interdependencies (e.g., `max_wal_senders` must be ≤ `max_connections`)

Test parameter changes in development environments before applying to production.

## Monitoring Parameter Changes

Parameter changes are logged in PostgreSQL logs:

```
LOG:  received SIGHUP, reloading configuration files
LOG:  parameter "work_mem" changed to "8MB"
```

You can query current settings:

```sql
-- Show all current settings
SELECT name, setting, unit, context FROM pg_settings ORDER BY name;

-- Show parameters requiring restart
SELECT name, setting FROM pg_settings WHERE context = 'postmaster';

-- Show pending restart parameters
SELECT name, setting, pending_restart FROM pg_settings WHERE pending_restart;
```

## Troubleshooting

### Parameters Not Taking Effect

1. **Check parameter context**: Some parameters require restart, not just reload
2. **Check Patroni**: Patroni writes `postgresql.auto.conf`, which overrides `postgresql.conf`
3. **Check logs**: PostgreSQL logs will show which parameters changed
4. **Verify spelling**: PostgreSQL accepts unknown parameters with warnings

### Performance Issues

1. **Check memory parameters**: Ensure `shared_buffers` and `effective_cache_size` are appropriate for your system
2. **Check WAL settings**: Undersized `max_wal_size` causes frequent checkpoints
3. **Check connection limits**: Too high `max_connections` wastes memory
4. **Check work_mem**: Too low causes disk sorts, too high causes OOM

### Replication Lag

1. **Check wal_level**: Must be `logical` or `replica` (operator sets `logical`)
2. **Check max_wal_senders**: Must be ≥ number of replicas
3. **Check wal_keep_size** (PG 13+) or **wal_keep_segments** (PG 12): Prevents WAL recycling
4. **Use replication slots**: Enabled by default via Patroni

## References

- [PostgreSQL Runtime Configuration Documentation](https://www.postgresql.org/docs/current/runtime-config.html)
- [PostgreSQL Server Configuration File](https://www.postgresql.org/docs/current/config-setting.html)
- [Patroni PostgreSQL Configuration](https://patroni.readthedocs.io/en/latest/SETTINGS.html#postgresql)
- [PGTune - PostgreSQL Configuration Wizard](https://pgtune.leopard.in.ua/)
- [Patroni Configuration Documentation](../patroni/config.md)
