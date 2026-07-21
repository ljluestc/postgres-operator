<!--
# Copyright 2025 Crunchy Data Solutions, Inc.
#
# SPDX-License-Identifier: Apache-2.0
-->

# PGO Troubleshooting Runbooks

This guide provides step-by-step troubleshooting procedures for common issues with the Postgres Operator (PGO).

## Table of Contents

1. [Cluster Won't Start](#cluster-wont-start)
2. [Backup Failures](#backup-failures)
3. [Restore Issues](#restore-issues)
4. [Replication Lag](#replication-lag)
5. [Failover Problems](#failover-problems)
6. [Connection Issues](#connection-issues)
7. [Storage Problems](#storage-problems)
8. [Certificate Errors](#certificate-errors)
9. [pgBouncer Issues](#pgbouncer-issues)
10. [Upgrade Failures](#upgrade-failures)

---

## Cluster Won't Start

### Symptoms
- Pods stuck in `Pending`, `CrashLoopBackOff`, or `Init` state
- PostgreSQL containers fail to start
- Cluster status shows not ready

### Diagnosis Steps

#### 1. Check Pod Status
```bash
kubectl get pods -l postgres-operator.crunchydata.com/cluster=<cluster-name>
kubectl describe pod <pod-name>
```

Look for:
- Image pull errors
- Resource constraints (CPU/memory)
- Volume mounting issues
- Init container failures

#### 2. Check Logs
```bash
# Database container logs
kubectl logs <pod-name> -c database

# Startup container logs
kubectl logs <pod-name> -c postgres-startup

# pgBackRest config logs
kubectl logs <pod-name> -c pgbackrest-config
```

#### 3. Check Events
```bash
kubectl get events --field-selector involvedObject.name=<pod-name> --sort-by='.lastTimestamp'
```

### Common Causes and Solutions

#### Image Pull Errors

**Symptoms:**
```
Failed to pull image: ImagePullBackOff
```

**Solutions:**
1. Verify image name and tag in PostgresCluster spec
2. Check imagePullSecrets configuration
3. Verify registry access and credentials
4. Check network connectivity to registry

```bash
# Test image pull manually
kubectl run test --image=<image> --image-pull-policy=Always -- sleep 1000
kubectl describe pod test
```

#### Insufficient Resources

**Symptoms:**
```
0/3 nodes are available: insufficient cpu/memory
```

**Solutions:**
1. Check node resources:
```bash
kubectl top nodes
kubectl describe nodes
```

2. Reduce resource requests in PostgresCluster spec:
```yaml
spec:
  instances:
    - resources:
        requests:
          cpu: 500m
          memory: 1Gi
```

3. Add more nodes to cluster or adjust requests/limits

#### Volume Mount Failures

**Symptoms:**
```
Unable to attach or mount volumes
```

**Solutions:**
1. Check PVC status:
```bash
kubectl get pvc -l postgres-operator.crunchydata.com/cluster=<cluster-name>
kubectl describe pvc <pvc-name>
```

2. Verify StorageClass exists and is available:
```bash
kubectl get storageclass
```

3. Check for node-specific volume issues:
```bash
kubectl describe node <node-name> | grep -A 10 "Events"
```

#### PostgreSQL Configuration Errors

**Symptoms:**
```
FATAL: configuration file contains errors
```

**Solutions:**
1. Check PostgreSQL logs for specific parameter errors
2. Review custom parameters in `spec.patroni.dynamicConfiguration.postgresql.parameters`
3. Verify parameter names and values are valid for your PostgreSQL version
4. Check for conflicting parameters

```bash
# View effective configuration
kubectl exec <pod-name> -c database -- cat /pgdata/pg16/postgresql.conf
```

---

## Backup Failures

### Symptoms
- Backup CronJobs fail
- pgBackRest backup repository shows errors
- Backup status not updating

### Diagnosis Steps

#### 1. Check Backup Job Status
```bash
kubectl get jobs -l postgres-operator.crunchydata.com/cluster=<cluster-name>
kubectl describe job <backup-job-name>
kubectl logs job/<backup-job-name>
```

#### 2. Check pgBackRest Repository Status
```bash
kubectl exec <instance-pod> -c database -- pgbackrest info
```

#### 3. Check Repository Configuration
```bash
# View pgBackRest configuration
kubectl get configmap <cluster-name>-pgbackrest-config -o yaml
```

### Common Causes and Solutions

#### Repository Not Initialized

**Symptoms:**
```
ERROR: [075]: repo1 is not initialized
```

**Solutions:**
```bash
# Manually create stanza
kubectl exec <instance-pod> -c database -- pgbackrest stanza-create --stanza=db

# Verify stanza creation
kubectl exec <instance-pod> -c database -- pgbackrest info
```

#### Cloud Storage Authentication Errors

**Symptoms:**
```
ERROR: [055]: unable to authenticate with cloud provider
```

**Solutions for S3:**
1. Verify AWS credentials secret exists:
```bash
kubectl get secret <cluster-name>-pgbackrest-secrets
kubectl describe secret <cluster-name>-pgbackrest-secrets
```

2. Check IAM permissions for S3 bucket access

3. Verify bucket name and region in PostgresCluster spec:
```yaml
spec:
  backups:
    pgbackrest:
      repos:
        - name: repo1
          s3:
            bucket: my-backup-bucket
            endpoint: s3.amazonaws.com
            region: us-east-1
```

**Solutions for GCS:**
1. Verify service account key secret
2. Check GCS bucket permissions
3. Verify bucket name in spec

**Solutions for Azure:**
1. Verify storage account credentials
2. Check container permissions
3. Verify container name in spec

#### Insufficient Disk Space

**Symptoms:**
```
ERROR: unable to write to repository: disk full
```

**Solutions:**
1. Check repository PVC size:
```bash
kubectl get pvc <cluster-name>-repo1 -o jsonpath='{.status.capacity.storage}'
```

2. Increase repository size:
```yaml
spec:
  backups:
    pgbackrest:
      repos:
        - name: repo1
          volume:
            volumeClaimSpec:
              resources:
                requests:
                  storage: 50Gi  # Increase size
```

3. Adjust retention policy to keep fewer backups:
```yaml
spec:
  backups:
    pgbackrest:
      global:
        repo1-retention-full: "7"
        repo1-retention-full-type: "time"
```

4. Manually expire old backups:
```bash
kubectl exec <instance-pod> -c database -- pgbackrest expire --stanza=db
```

#### Network Issues

**Symptoms:**
```
ERROR: [041]: unable to connect to repository host
```

**Solutions:**
1. Verify repo host pod is running:
```bash
kubectl get pod <cluster-name>-repo-host-0
```

2. Check TLS certificates are valid:
```bash
kubectl get secret <cluster-name>-replication-cert -o yaml
```

3. Test connectivity:
```bash
kubectl exec <instance-pod> -c database -- pgbackrest server-ping
```

---

## Restore Issues

### Symptoms
- Restore job fails
- Data directory not populated
- Cluster stuck in recovery mode

### Full Backup/Restore with pgBackRest (S3-Compatible Repositories)

If you want a self-contained full restore workflow (similar in intent to a full
logical dump/restore cycle, but physical and pgBackRest-based), you can do it in
either of these two ways:

1. **Create a new cluster from a pgBackRest repository** (clone-style restore)
2. **Run an in-place restore on an existing cluster**

Both approaches work with `spec.backups.pgbackrest` repositories, including S3.

#### Option A: Create a New Cluster from Existing Backups

Use `spec.dataSource.postgresCluster` to bootstrap a new `PostgresCluster` from
backups in a source cluster repository.

```yaml
apiVersion: postgres-operator.crunchydata.com/v1beta1
kind: PostgresCluster
metadata:
  name: restored-cluster
spec:
  postgresVersion: 16
  dataSource:
    postgresCluster:
      clusterName: source-cluster
      repoName: repo1
      # Optional restore options:
      # options:
      #   - --type=default
  instances:
    - name: instance1
      replicas: 1
      dataVolumeClaimSpec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
  backups:
    pgbackrest:
      repos:
        - name: repo1
          s3:
            bucket: my-backup-bucket
            endpoint: s3.amazonaws.com
            region: us-east-1
```

#### Option B: In-Place Restore Using `spec.backups.pgbackrest.restore`

For an existing cluster, enable in-place restore and set the restore source repo.
For a full restore target, use default restore semantics (latest valid backup set).

```yaml
spec:
  backups:
    pgbackrest:
      restore:
        enabled: true
        repoName: repo1
        # Optional restore options for explicit control:
        # options:
        #   - --type=default
```

#### Notes

- Full restores with pgBackRest are **physical** restores (cluster-level data
  files), not logical exports like `pg_dump`.
- You do **not** need PITR options (`--type=time`, `--target=...`) for a standard
  full restore.
- Ensure `repoName` points at the repository containing the desired full backup
  chain.

### Diagnosis Steps

#### 1. Check Restore Configuration
```bash
kubectl get postgrescluster <cluster-name> -o yaml | yq '.spec.backups.pgbackrest.restore'
```

#### 2. Check Restore Job Logs
```bash
# Find restore job
kubectl get jobs -l postgres-operator.crunchydata.com/pgbackrest-restore=true

# Check logs
kubectl logs job/<restore-job-name>
```

#### 3. Check Database Status
```bash
kubectl exec <instance-pod> -c database -- psql -c "SELECT pg_is_in_recovery();"
```

### Common Causes and Solutions

#### Backup Not Found

**Symptoms:**
```
ERROR: unable to find backup for restore
```

**Solutions:**
1. List available backups:
```bash
kubectl exec <instance-pod> -c database -- pgbackrest info
```

2. Verify restore target in spec matches available backup
3. For PITR, ensure target timestamp is valid:
```yaml
spec:
  backups:
    pgbackrest:
      restore:
        enabled: true
        repoName: repo1
        options:
          - --type=time
          - --target="2025-01-15 10:30:00"
```

#### Restore Permissions Errors

**Symptoms:**
```
ERROR: unable to restore: permission denied
```

**Solutions:**
1. Check pod security context
2. Verify PVC permissions
3. Ensure restore job has correct service account

#### Incomplete Restore

**Symptoms:**
- Restore job succeeds but data is missing
- Database starts but tables are empty

**Solutions:**
1. Check restore logs for warnings
2. Verify backup integrity:
```bash
kubectl exec <instance-pod> -c database -- pgbackrest verify --stanza=db
```

3. Try delta restore if using volume snapshots:
```yaml
spec:
  backups:
    pgbackrest:
      restore:
        enabled: true
        repoName: repo1
        options:
          - --delta
```

---

## Replication Lag

### Symptoms
- High `pg_stat_replication.replay_lag`
- Replicas significantly behind primary
- Monitoring alerts for replication lag

### Diagnosis Steps

#### 1. Check Current Lag
```bash
kubectl exec <primary-pod> -c database -- psql -c "
  SELECT
    client_addr,
    application_name,
    state,
    sent_lsn,
    write_lsn,
    flush_lsn,
    replay_lsn,
    sync_state,
    replay_lag
  FROM pg_stat_replication;
"
```

#### 2. Check WAL Status
```bash
kubectl exec <primary-pod> -c database -- psql -c "
  SELECT pg_current_wal_lsn(), pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn) AS lag_bytes
  FROM pg_stat_replication;
"
```

#### 3. Check Replication Slots
```bash
kubectl exec <primary-pod> -c database -- psql -c "
  SELECT slot_name, active, restart_lsn, confirmed_flush_lsn
  FROM pg_replication_slots;
"
```

### Common Causes and Solutions

#### High Write Load on Primary

**Symptoms:**
- WAL generation rate exceeds replica replay rate
- `replay_lag` steadily increasing

**Solutions:**
1. Increase replica resources:
```yaml
spec:
  instances:
    - resources:
        limits:
          cpu: 2000m
          memory: 4Gi
```

2. Tune checkpoint settings:
```yaml
spec:
  patroni:
    dynamicConfiguration:
      postgresql:
        parameters:
          max_wal_size: "4GB"
          checkpoint_completion_target: "0.9"
```

3. Optimize queries to reduce write load
4. Consider read-only routing for queries

#### Network Issues

**Symptoms:**
- Intermittent lag spikes
- Disconnections in `pg_stat_replication`

**Solutions:**
1. Check pod networking:
```bash
kubectl exec <primary-pod> -c database -- ping <replica-pod-ip>
```

2. Review network policies
3. Check for bandwidth constraints
4. Monitor network latency

#### Replica Resource Starvation

**Symptoms:**
- High CPU or I/O on replica
- Slow query execution on replica

**Solutions:**
1. Check replica resource usage:
```bash
kubectl top pod <replica-pod>
```

2. Identify blocking queries:
```bash
kubectl exec <replica-pod> -c database -- psql -c "
  SELECT pid, usename, application_name, wait_event_type, wait_event, query
  FROM pg_stat_activity
  WHERE wait_event IS NOT NULL;
"
```

3. Enable `hot_standby_feedback` to prevent query cancellations:
```yaml
spec:
  patroni:
    dynamicConfiguration:
      postgresql:
        parameters:
          hot_standby_feedback: "on"
```

#### Inactive Replication Slot

**Symptoms:**
- Replication slot exists but not active
- WAL accumulation on primary

**Solutions:**
1. Check if replica is running:
```bash
kubectl get pod <replica-pod>
```

2. Restart replica if necessary:
```bash
kubectl delete pod <replica-pod>
```

3. Drop and recreate slot if corrupted (Patroni manages this automatically)

---

## Failover Problems

### Symptoms
- Failover doesn't occur when primary fails
- Multiple primaries detected (split-brain)
- Replicas don't promote to primary

### Diagnosis Steps

#### 1. Check Patroni Status
```bash
kubectl exec <any-instance-pod> -- patronictl list
```

#### 2. Check Cluster Leader
```bash
kubectl get configmap <cluster-name>-config -o yaml | grep "leader:"
```

#### 3. Check Pod Readiness
```bash
kubectl get pods -l postgres-operator.crunchydata.com/cluster=<cluster-name> -o wide
```

### Common Causes and Solutions

#### Insufficient Replicas for Quorum

**Symptoms:**
```
No eligible candidates for leader election
```

**Solutions:**
1. Ensure at least 2 healthy instances exist
2. Check replica count in spec:
```yaml
spec:
  instances:
    - replicas: 3  # Minimum 2 for HA
```

3. Fix unhealthy replicas before attempting failover

#### Network Partitions

**Symptoms:**
- Multiple instances think they are primary
- Split-brain scenario

**Solutions:**
1. Check network connectivity between pods:
```bash
for pod in $(kubectl get pods -l postgres-operator.crunchydata.com/cluster=<cluster-name> -o name); do
  kubectl exec $pod -- ping -c 1 <other-pod-ip>
done
```

2. Verify no network policies blocking inter-pod communication
3. Check pod anti-affinity to ensure pods are on different nodes
4. Review Kubernetes API server health

#### Patroni Configuration Issues

**Symptoms:**
- Failover times out
- Patroni unable to update DCS

**Solutions:**
1. Check Patroni parameters:
```yaml
spec:
  patroni:
    leaderLeaseDurationSeconds: 30
    syncPeriodSeconds: 10
```

2. Increase TTL if frequent leader changes:
```yaml
spec:
  patroni:
    dynamicConfiguration:
      ttl: 30
      loop_wait: 10
      retry_timeout: 10
```

3. Check Patroni logs:
```bash
kubectl logs <instance-pod> -c database | grep patroni
```

#### Replica Not Eligible for Promotion

**Symptoms:**
```
Replica has nofailover tag
```

**Solutions:**
1. Check replica tags in Patroni configuration
2. Ensure replica is not too far behind (check `maximum_lag_on_failover`)
3. Verify replica health:
```bash
kubectl exec <replica-pod> -c database -- psql -c "SELECT pg_is_in_recovery(), pg_last_wal_receive_lsn();"
```

---

## Connection Issues

### Symptoms
- Applications cannot connect to PostgreSQL
- Connection timeouts
- Authentication failures

### Diagnosis Steps

#### 1. Check Service Endpoints
```bash
kubectl get svc -l postgres-operator.crunchydata.com/cluster=<cluster-name>
kubectl get endpoints <cluster-name>-primary
```

#### 2. Test Connection from Pod
```bash
kubectl run -it --rm psql-test --image=postgres:16 --restart=Never -- \
  psql "host=<cluster-name>-primary.default.svc port=5432 user=postgres sslmode=require dbname=postgres"
```

#### 3. Check pg_hba Configuration
```bash
kubectl exec <instance-pod> -c database -- cat /pgdata/pg16/pg_hba.conf
```

### Common Causes and Solutions

#### Service Not Ready

**Symptoms:**
```
No endpoints available for service
```

**Solutions:**
1. Check if pods are ready:
```bash
kubectl get pods -l postgres-operator.crunchydata.com/cluster=<cluster-name>
```

2. Check pod readiness probe:
```bash
kubectl describe pod <instance-pod> | grep -A 5 "Readiness"
```

3. Check database is accepting connections:
```bash
kubectl exec <instance-pod> -c database -- pg_isready
```

#### Authentication Failures

**Symptoms:**
```
FATAL: password authentication failed
FATAL: no pg_hba.conf entry for host
```

**Solutions:**
1. Verify credentials in secret:
```bash
kubectl get secret <cluster-name>-pguser-<username> -o jsonpath='{.data.password}' | base64 -d
```

2. Check pg_hba rules allow connection:
```yaml
spec:
  patroni:
    dynamicConfiguration:
      postgresql:
        pg_hba:
          - host all all 0.0.0.0/0 scram-sha-256
```

3. Verify SSL mode matches requirements:
```bash
# If using sslmode=require, certificate must be valid
kubectl get secret <cluster-name>-cluster-cert -o yaml
```

#### TLS Certificate Issues

**Symptoms:**
```
SSL error: certificate verify failed
```

**Solutions:**
1. Check certificate validity:
```bash
kubectl get secret <cluster-name>-cluster-cert -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -dates
```

2. Regenerate certificates if expired:
```bash
kubectl delete secret <cluster-name>-cluster-cert
# Operator will regenerate automatically
```

3. Use correct sslmode for client:
- `require`: Encrypt but don't verify certificate
- `verify-ca`: Encrypt and verify CA
- `verify-full`: Encrypt and verify full certificate chain

#### Connection Pooler (pgBouncer) Issues

**Symptoms:**
- Connections work to PostgreSQL directly but not through pgBouncer
- Connection pool exhausted

**Solutions:**
1. Check pgBouncer status:
```bash
kubectl get pods -l postgres-operator.crunchydata.com/role=pgbouncer
kubectl logs <pgbouncer-pod>
```

2. Check connection limits:
```yaml
spec:
  proxy:
    pgBouncer:
      config:
        global:
          max_client_conn: "1000"
          default_pool_size: "25"
```

3. Monitor pool usage:
```bash
kubectl exec <pgbouncer-pod> -- psql -p 5432 pgbouncer -c "SHOW POOLS;"
```

---

## Storage Problems

### Symptoms
- Pods stuck due to PVC issues
- Disk full errors
- Storage performance degradation

### Diagnosis Steps

#### 1. Check PVC Status
```bash
kubectl get pvc -l postgres-operator.crunchydata.com/cluster=<cluster-name>
kubectl describe pvc <pvc-name>
```

#### 2. Check Disk Usage
```bash
kubectl exec <instance-pod> -c database -- df -h /pgdata
```

#### 3. Check Storage Class
```bash
kubectl get storageclass
kubectl describe storageclass <storage-class-name>
```

### Common Causes and Solutions

#### PVC Pending

**Symptoms:**
```
Waiting for volume to be created
```

**Solutions:**
1. Check storage provisioner logs
2. Verify StorageClass has a provisioner
3. Check node capacity for local volumes
4. Verify cloud provider permissions for dynamic provisioning

#### Disk Full

**Symptoms:**
```
ERROR: could not write to file: No space left on device
```

**Solutions:**
1. Identify what's consuming space:
```bash
kubectl exec <instance-pod> -c database -- du -sh /pgdata/*
```

2. Common space consumers:
- WAL files: Check `max_wal_size` and archiving status
- Temp files: Check for long-running queries
- Logs: Implement log rotation
- Tables: Run VACUUM FULL on bloated tables

3. Increase PVC size (if storage class supports expansion):
```yaml
spec:
  instances:
    - dataVolumeClaimSpec:
        resources:
          requests:
            storage: 50Gi  # Increase from 20Gi
```

4. Enable auto-grow for dynamic expansion:
```yaml
spec:
  instances:
    - dataVolumeClaimSpec:
        autoGrow:
          trigger: 80
          maxGrow: 10Gi
```

#### Volume Mount Errors

**Symptoms:**
```
Unable to mount volume: already attached to another node
```

**Solutions:**
1. For ReadWriteOnce volumes, ensure pod is deleted before rescheduling
2. Force delete stuck pod:
```bash
kubectl delete pod <pod-name> --force --grace-period=0
```

3. Check for underlying volume issues in cloud provider

#### Performance Issues

**Symptoms:**
- Slow query execution
- High I/O wait
- Checkpoint warnings in logs

**Solutions:**
1. Check storage class performance characteristics
2. Use higher-performance storage (e.g., SSD instead of HDD)
3. Tune PostgreSQL parameters:
```yaml
spec:
  patroni:
    dynamicConfiguration:
      postgresql:
        parameters:
          random_page_cost: "1.1"  # For SSD
          effective_io_concurrency: "200"
```

4. Monitor I/O metrics:
```bash
kubectl top pod <instance-pod> --containers
```

---

## Certificate Errors

### Symptoms
- TLS handshake failures
- Certificate verification errors
- Pods failing to start due to cert issues

### Diagnosis Steps

#### 1. Check Certificate Secrets
```bash
kubectl get secrets -l postgres-operator.crunchydata.com/cluster=<cluster-name>
kubectl describe secret <cluster-name>-cluster-cert
```

#### 2. Verify Certificate Validity
```bash
kubectl get secret <cluster-name>-cluster-cert -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -text
```

#### 3. Check Certificate Dates
```bash
kubectl get secret <cluster-name>-cluster-cert -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -dates
```

### Common Causes and Solutions

#### Expired Certificates

**Symptoms:**
```
SSL error: certificate has expired
```

**Solutions:**
1. Delete expired certificate secret:
```bash
kubectl delete secret <cluster-name>-cluster-cert
```

2. Operator will regenerate automatically
3. Pods may need restart to pick up new certificate:
```bash
kubectl delete pod -l postgres-operator.crunchydata.com/cluster=<cluster-name>
```

#### Certificate Name Mismatch

**Symptoms:**
```
SSL error: certificate does not match hostname
```

**Solutions:**
1. Verify certificate Subject Alternative Names (SANs):
```bash
kubectl get secret <cluster-name>-cluster-cert -o jsonpath='{.data.tls\.crt}' | \
  base64 -d | openssl x509 -noout -text | grep -A1 "Subject Alternative Name"
```

2. Use custom certificate with correct SANs if needed:
```yaml
spec:
  customTLSSecret:
    name: my-custom-cert
```

#### CA Certificate Issues

**Symptoms:**
```
SSL error: unable to verify the first certificate
```

**Solutions:**
1. Check CA certificate:
```bash
kubectl get secret <cluster-name>-cluster-cert -o jsonpath='{.data.ca\.crt}' | base64 -d | openssl x509 -noout -text
```

2. Ensure clients have correct CA for verification
3. For custom certificates, ensure CA chain is complete

---

## pgBouncer Issues

### Symptoms
- Connection pooler not routing traffic
- Pool exhaustion errors
- Authentication failures through pgBouncer

### Diagnosis Steps

#### 1. Check pgBouncer Status
```bash
kubectl get pods -l postgres-operator.crunchydata.com/role=pgbouncer
kubectl logs <pgbouncer-pod>
```

#### 2. Check pgBouncer Configuration
```bash
kubectl exec <pgbouncer-pod> -- cat /etc/pgbouncer/pgbouncer.ini
```

#### 3. Check Pool Status
```bash
kubectl exec <pgbouncer-pod> -- psql -p 5432 pgbouncer -c "SHOW POOLS;"
kubectl exec <pgbouncer-pod> -- psql -p 5432 pgbouncer -c "SHOW DATABASES;"
kubectl exec <pgbouncer-pod> -- psql -p 5432 pgbouncer -c "SHOW CLIENTS;"
```

### Common Causes and Solutions

#### Pool Exhaustion

**Symptoms:**
```
no more connections allowed (max_client_conn)
ERROR:  all server connections are in use
```

**Solutions:**
1. Increase connection limits:
```yaml
spec:
  proxy:
    pgBouncer:
      config:
        global:
          max_client_conn: "2000"
          default_pool_size: "50"
```

2. Tune pool mode for your workload:
```yaml
spec:
  proxy:
    pgBouncer:
      config:
        global:
          pool_mode: "transaction"  # or "session", "statement"
```

3. Scale pgBouncer replicas:
```yaml
spec:
  proxy:
    pgBouncer:
      replicas: 3
```

#### Backend Connection Issues

**Symptoms:**
```
ERROR:  cannot connect to server
```

**Solutions:**
1. Verify PostgreSQL service is accessible:
```bash
kubectl get svc <cluster-name>-primary
```

2. Check pgBouncer can reach PostgreSQL:
```bash
kubectl exec <pgbouncer-pod> -- psql -h <cluster-name>-primary -p 5432 -U postgres
```

3. Review pgBouncer logs for connection errors

#### Authentication Failures

**Symptoms:**
```
ERROR:  authentication failed for user
```

**Solutions:**
1. Verify userlist configuration:
```bash
kubectl exec <pgbouncer-pod> -- cat /etc/pgbouncer/users.txt
```

2. Ensure passwords are synchronized between PostgreSQL and pgBouncer
3. Check authentication type in configuration:
```yaml
spec:
  proxy:
    pgBouncer:
      config:
        global:
          auth_type: "scram-sha-256"
```

---

## Upgrade Failures

### Symptoms
- PGUpgrade stuck or failing
- Incompatibility errors during upgrade
- Data directory corruption after upgrade

### Diagnosis Steps

#### 1. Check PGUpgrade Status
```bash
kubectl get pgupgrade <upgrade-name> -o yaml
kubectl describe pgupgrade <upgrade-name>
```

#### 2. Check Upgrade Job Logs
```bash
kubectl get jobs -l postgres-operator.crunchydata.com/pgupgrade=<upgrade-name>
kubectl logs job/<upgrade-job-name>
```

#### 3. Check PostgreSQL Version
```bash
kubectl exec <instance-pod> -c database -- psql -c "SELECT version();"
```

### Common Causes and Solutions

#### Version Incompatibility

**Symptoms:**
```
ERROR: incompatible library version
ERROR: extension version mismatch
```

**Solutions:**
1. Verify upgrade path is supported (e.g., PG 15 → 16, not 14 → 18)
2. Update extensions before major upgrade:
```bash
kubectl exec <instance-pod> -c database -- psql -c "ALTER EXTENSION <extension> UPDATE;"
```

3. Check extension compatibility with target version
4. Review release notes for breaking changes

#### Insufficient Disk Space

**Symptoms:**
```
ERROR: could not create file: No space left on device
```

**Solutions:**
1. Ensure enough space for upgrade (typically 2x data directory size)
2. Increase PVC size before upgrade
3. Clean up unnecessary data:
```bash
kubectl exec <instance-pod> -c database -- psql -c "VACUUM FULL;"
```

#### Configuration Incompatibilities

**Symptoms:**
```
FATAL: configuration parameter removed in version X
```

**Solutions:**
1. Review deprecated parameters for target version
2. Update PostgresCluster spec to remove deprecated parameters:
```yaml
spec:
  patroni:
    dynamicConfiguration:
      postgresql:
        parameters:
          # Remove deprecated parameters
          # Add new equivalents if needed
```

3. Test configuration on target version before upgrading production

---

## General Troubleshooting Tips

### Enable Debug Logging

For operator logs:
```bash
# Check operator logs
kubectl logs -n postgres-operator deployment/pgo -f

# Increase operator verbosity (if supported)
kubectl set env deployment/pgo -n postgres-operator OPERATOR_LOG_LEVEL=debug
```

For Patroni logs:
```bash
kubectl exec <instance-pod> -c database -- patronictl reload --log-level=DEBUG <cluster-name>
```

### Collect Diagnostic Information

Create a diagnostic bundle:
```bash
#!/bin/bash
CLUSTER=<cluster-name>
mkdir -p diagnostics/$CLUSTER

# Cluster definition
kubectl get postgrescluster $CLUSTER -o yaml > diagnostics/$CLUSTER/cluster.yaml

# Pod status
kubectl get pods -l postgres-operator.crunchydata.com/cluster=$CLUSTER -o wide > diagnostics/$CLUSTER/pods.txt

# Pod logs
for pod in $(kubectl get pods -l postgres-operator.crunchydata.com/cluster=$CLUSTER -o name); do
  kubectl logs $pod --all-containers > diagnostics/$CLUSTER/$(basename $pod).log
done

# Events
kubectl get events --field-selector involvedObject.name=$CLUSTER > diagnostics/$CLUSTER/events.txt

# PVCs
kubectl get pvc -l postgres-operator.crunchydata.com/cluster=$CLUSTER -o yaml > diagnostics/$CLUSTER/pvcs.yaml

# Services
kubectl get svc -l postgres-operator.crunchydata.com/cluster=$CLUSTER -o yaml > diagnostics/$CLUSTER/services.yaml

# ConfigMaps
kubectl get cm -l postgres-operator.crunchydata.com/cluster=$CLUSTER -o yaml > diagnostics/$CLUSTER/configmaps.yaml

# Secrets (metadata only)
kubectl get secrets -l postgres-operator.crunchydata.com/cluster=$CLUSTER -o yaml | kubectl neat > diagnostics/$CLUSTER/secrets.yaml

# Patroni status
kubectl exec ${CLUSTER}-instance1-0 -- patronictl list > diagnostics/$CLUSTER/patroni-status.txt

tar -czf diagnostics-${CLUSTER}-$(date +%Y%m%d-%H%M%S).tar.gz diagnostics/$CLUSTER/
```

### Common kubectl Commands

```bash
# Watch cluster status
watch kubectl get postgrescluster <cluster-name>

# Follow logs
kubectl logs <pod-name> -c database -f

# Execute SQL
kubectl exec <pod-name> -c database -- psql -c "SELECT ..."

# Port forward for local access
kubectl port-forward svc/<cluster-name>-primary 5432:5432

# Describe resource for events
kubectl describe postgrescluster <cluster-name>

# Check operator logs
kubectl logs -n postgres-operator deployment/pgo --tail=100 -f
```

## Getting Help

If these troubleshooting steps don't resolve your issue:

1. **Check Documentation**: Review [PGO documentation](https://access.crunchydata.com/documentation/postgres-operator/)
2. **Search Issues**: Check [GitHub Issues](https://github.com/CrunchyData/postgres-operator/issues)
3. **Community Support**: Join [PGO Discord](https://discord.gg/crunchydata)
4. **File a Bug**: Create a [new issue](https://github.com/CrunchyData/postgres-operator/issues/new) with diagnostic information
5. **Commercial Support**: Contact [Crunchy Data Support](https://www.crunchydata.com/products/crunchy-bridge/) for enterprise support
