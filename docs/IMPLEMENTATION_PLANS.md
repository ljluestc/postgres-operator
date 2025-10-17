<!--
# Copyright 2025 Crunchy Data Solutions, Inc.
#
# SPDX-License-Identifier: Apache-2.0
-->

# Implementation Plans for PGO Features

This document outlines detailed implementation plans for new features identified in the PRD (Product Requirements Document).

## Table of Contents

1. [Backup Verification Automation](#1-backup-verification-automation)
2. [Enhanced Backup Metrics and Monitoring](#2-enhanced-backup-metrics-and-monitoring)
3. [Automated Secrets Rotation](#3-automated-secrets-rotation)
4. [FIPS Mode Support](#4-fips-mode-support)
5. [Disaster Recovery Drill Automation](#5-disaster-recovery-drill-automation)
6. [Failover Time Optimization](#6-failover-time-optimization)
7. [Interactive Cluster Creation Wizard](#7-interactive-cluster-creation-wizard)
8. [Query Performance Insights](#8-query-performance-insights)
9. [Connection Pool Analytics](#9-connection-pool-analytics)
10. [Backup Encryption at Rest](#10-backup-encryption-at-rest)
11. [Cross-Region Backup Replication](#11-cross-region-backup-replication)
12. [Configuration Validation Framework](#12-configuration-validation-framework)
13. [Auto-Scaling Read Replicas](#13-auto-scaling-read-replicas)

---

## 1. Backup Verification Automation

### Overview
Automatically verify backup integrity on a regular schedule to ensure backups are restorable.

### Goals
- Prevent "backup exists but won't restore" scenarios
- Provide confidence in disaster recovery capabilities
- Alert on verification failures

### Design

#### API Changes
Add to `PGBackRestArchive` spec:

```go
type PGBackRestArchive struct {
    // ... existing fields ...

    // Verification settings
    Verification *BackupVerification `json:"verification,omitempty"`
}

type BackupVerification struct {
    // Enable automatic backup verification
    Enabled bool `json:"enabled"`

    // Schedule for verification (cron format)
    // +kubebuilder:validation:Pattern=`^(@(annually|yearly|monthly|weekly|daily|hourly))$|^((((\d+,)+\d+|(\d+(\/|-)\d+)|\d+|\*) ?){5})$`
    Schedule string `json:"schedule"`

    // Repository to verify
    RepoName string `json:"repoName"`

    // Verification method
    // +kubebuilder:validation:Enum=restore-test;checksum;both
    Method string `json:"method,omitempty"` // Default: checksum

    // Resources for verification job
    Resources corev1.ResourceRequirements `json:"resources,omitempty"`

    // Verification timeout
    // +kubebuilder:validation:Type=string
    // +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
    Timeout *metav1.Duration `json:"timeout,omitempty"`
}
```

#### Implementation Steps

**Phase 1: Checksum Verification (Low Risk)**
1. Create CronJob for running `pgbackrest verify`
2. Report verification status in cluster status
3. Generate Kubernetes events for failures
4. Add Prometheus metrics for verification results

**Phase 2: Restore Test Verification (Higher Risk)**
1. Create ephemeral namespace for restore testing
2. Perform delta restore to test volume
3. Verify PostgreSQL can start and queries work
4. Clean up test resources after verification
5. Report detailed results

#### Files to Modify/Create
- `pkg/apis/postgres-operator.crunchydata.com/v1beta1/pgbackrest_types.go`
- `internal/pgbackrest/verify.go` (new)
- `internal/controller/postgrescluster/pgbackrest_verify.go` (new)

#### Testing Strategy
- Unit tests for verification logic
- Integration tests with actual backup/restore
- E2E test for full verification workflow

#### Risks and Mitigations
- **Risk**: Restore test consumes significant resources
  - **Mitigation**: Make it optional, default to checksum only
- **Risk**: Verification failures cause alert fatigue
  - **Mitigation**: Provide detailed error messages and debugging info

---

## 2. Enhanced Backup Metrics and Monitoring

### Overview
Expand Prometheus metrics for backup operations to provide better observability.

### Goals
- Track backup success/failure rates over time
- Monitor backup duration and size trends
- Alert on backup anomalies

### Design

#### New Metrics

```go
var (
    backupDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "pgo_backup_duration_seconds",
            Help: "Duration of backup operations",
            Buckets: prometheus.ExponentialBuckets(60, 2, 10), // 60s to ~17 hours
        },
        []string{"cluster", "namespace", "repo", "type"}, // type: full, diff, incr
    )

    backupSize = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "pgo_backup_size_bytes",
            Help: "Size of backup in bytes",
        },
        []string{"cluster", "namespace", "repo", "type"},
    )

    backupFailures = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "pgo_backup_failures_total",
            Help: "Total number of backup failures",
        },
        []string{"cluster", "namespace", "repo", "reason"},
    )

    backupAge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "pgo_backup_age_seconds",
            Help: "Age of the latest successful backup",
        },
        []string{"cluster", "namespace", "repo"},
    )

    backupCount = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "pgo_backup_count",
            Help: "Number of backups in repository",
        },
        []string{"cluster", "namespace", "repo", "type"},
    )

    repositorySize = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "pgo_repository_size_bytes",
            Help: "Total size of backup repository",
        },
        []string{"cluster", "namespace", "repo"},
    )

    repositoryUtilization = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "pgo_repository_utilization_percent",
            Help: "Repository disk utilization percentage",
        },
        []string{"cluster", "namespace", "repo"},
    )
)
```

#### Implementation Steps

1. **Parse pgBackRest Output**
   - Create parser for `pgbackrest info --output=json`
   - Extract metrics from backup job logs

2. **Update Status Tracking**
   - Enhance `PGBackRestRepo` status to include:
     - Last backup timestamp
     - Backup count by type
     - Repository size
     - Error counts

3. **Export Metrics**
   - Add metrics collection in backup reconciler
   - Update metrics after backup completion
   - Create Grafana dashboard template

4. **Alerting Rules**
   ```yaml
   # Example Prometheus alerting rules
   groups:
     - name: pgo_backups
       rules:
         - alert: PGOBackupFailed
           expr: pgo_backup_failures_total > 0
           for: 5m
           labels:
             severity: critical
           annotations:
             summary: "PostgreSQL backup failed"
             description: "Backup for {{ $labels.cluster }} failed"

         - alert: PGOBackupAge
           expr: pgo_backup_age_seconds > 86400  # 24 hours
           for: 1h
           labels:
             severity: warning
           annotations:
             summary: "PostgreSQL backup is old"
             description: "Last backup for {{ $labels.cluster }} is over 24 hours old"
   ```

#### Files to Modify/Create
- `internal/pgbackrest/metrics.go` (new)
- `internal/pgbackrest/reconcile.go`
- `internal/controller/postgrescluster/pgbackrest.go`
- `docs/grafana/backup-dashboard.json` (new)

---

## 3. Automated Secrets Rotation

### Overview
Automatically rotate PostgreSQL passwords and certificates on a configurable schedule.

### Goals
- Improve security posture with regular credential rotation
- Minimize manual intervention
- Zero-downtime rotation where possible

### Design

#### API Changes

```go
type PostgresClusterSpec struct {
    // ... existing fields ...

    // Secrets rotation configuration
    SecretsRotation *SecretsRotation `json:"secretsRotation,omitempty"`
}

type SecretsRotation struct {
    // Enable automatic secrets rotation
    Enabled bool `json:"enabled"`

    // Password rotation settings
    Passwords *PasswordRotation `json:"passwords,omitempty"`

    // Certificate rotation settings
    Certificates *CertificateRotation `json:"certificates,omitempty"`
}

type PasswordRotation struct {
    // Rotation interval
    // +kubebuilder:validation:Type=string
    // +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h|d))+$"
    Interval *metav1.Duration `json:"interval"` // e.g., "90d"

    // Users to rotate (empty = all non-system users)
    Users []string `json:"users,omitempty"`

    // Exclude these users from rotation
    ExcludeUsers []string `json:"excludeUsers,omitempty"`
}

type CertificateRotation struct {
    // Days before expiration to trigger rotation
    RenewBefore int `json:"renewBefore"` // e.g., 30 days

    // Rotation method
    // +kubebuilder:validation:Enum=rolling;immediate
    Method string `json:"method,omitempty"` // Default: rolling
}
```

#### Implementation Steps

**Phase 1: Password Rotation**

1. **Track Password Age**
   - Add annotation to secrets with creation/rotation timestamp
   - Check age during reconciliation

2. **Generate New Password**
   - Use crypto/rand for secure password generation
   - Apply password policies (length, complexity)

3. **Update PostgreSQL**
   ```sql
   ALTER USER username WITH PASSWORD 'new_password';
   ```

4. **Update Secret**
   - Update secret with new password
   - Add annotation with rotation timestamp
   - Emit event for rotation completion

5. **Application Handling**
   - Applications using secrets should watch for changes
   - Document best practices for password rotation handling

**Phase 2: Certificate Rotation**

1. **Monitor Certificate Expiration**
   - Check certificate validity daily
   - Calculate days until expiration

2. **Generate New Certificates**
   - Create new certificate with same CN/SANs
   - Sign with same CA (or new CA if rotating root)

3. **Rolling Update**
   - Update secrets with new certificates
   - Restart pods one at a time to pick up new certs
   - Wait for pod to be ready before proceeding

4. **Verification**
   - Verify TLS connections work with new certificates
   - Check Patroni connectivity
   - Verify replication is functioning

#### Files to Modify/Create
- `pkg/apis/postgres-operator.crunchydata.com/v1beta1/postgrescluster_types.go`
- `internal/controller/postgrescluster/secrets_rotation.go` (new)
- `internal/postgres/passwords.go` (enhance)
- `internal/certificates/rotation.go` (new)

#### Testing Strategy
- Unit tests for password generation
- Integration tests for password rotation
- E2E tests for certificate rotation with connection validation

#### Risks and Mitigations
- **Risk**: Applications not updated with new password
  - **Mitigation**: Provide dual-password period (old + new valid)
- **Risk**: Certificate rotation causes connection failures
  - **Mitigation**: Rolling update, verify each step
- **Risk**: Rotation during high load causes issues
  - **Mitigation**: Add maintenance window configuration

---

## 4. FIPS Mode Support

### Overview
Enable FIPS 140-2 compliant cryptography for regulated environments.

### Goals
- Support government and regulated industry requirements
- Use FIPS-validated cryptographic modules
- Maintain performance where possible

### Design

#### Requirements Analysis
- PostgreSQL built with OpenSSL FIPS module
- Container images with FIPS-enabled OS (RHEL, Rocky Linux)
- All TLS using FIPS-approved ciphers
- Password hashing using FIPS-approved algorithms (SCRAM-SHA-256)

#### API Changes

```go
type PostgresClusterSpec struct {
    // ... existing fields ...

    // FIPS mode configuration
    FIPS *FIPSConfig `json:"fips,omitempty"`
}

type FIPSConfig struct {
    // Enable FIPS mode
    Enabled bool `json:"enabled"`

    // Enforce FIPS validation (fail if FIPS not available)
    Enforce bool `json:"enforce,omitempty"` // Default: true if enabled
}
```

#### Implementation Steps

1. **Container Images**
   - Create FIPS-enabled base images
   - Build PostgreSQL with `--with-openssl` and FIPS module
   - Build pgBackRest with FIPS-compliant crypto

2. **System Configuration**
   - Enable FIPS mode in container init:
     ```bash
     fips-mode-setup --enable
     update-crypto-policies --set FIPS
     ```

3. **PostgreSQL Configuration**
   ```yaml
   parameters:
     ssl_ciphers: "FIPS"  # Use FIPS-approved ciphers
     password_encryption: "scram-sha-256"  # FIPS-approved
   ```

4. **Certificate Generation**
   - Use FIPS-approved key sizes (RSA 2048+, ECDSA P-256+)
   - FIPS-approved signature algorithms (SHA-256+)

5. **Validation**
   - Add startup checks for FIPS mode
   - Verify OpenSSL FIPS mode enabled
   - Test that non-FIPS algorithms are rejected

#### Files to Modify/Create
- `build/Dockerfile.fips` (new)
- `internal/fips/validation.go` (new)
- `internal/controller/postgrescluster/fips.go` (new)
- `internal/certificates/fips.go` (enhance)

#### Testing Strategy
- Unit tests for FIPS validation logic
- Integration tests in FIPS-enabled environment
- Compliance testing with FIPS validation tools
- Performance benchmarks (FIPS vs non-FIPS)

#### Documentation Needs
- FIPS deployment guide
- Compliance certification documentation
- Performance impact analysis
- Migration path from non-FIPS to FIPS

---

## 5. Disaster Recovery Drill Automation

### Overview
Automate disaster recovery drills to validate backup/restore procedures.

### Goals
- Regular testing of DR procedures
- Automated validation of restore success
- Metrics on RTO (Recovery Time Objective)

### Design

#### API Changes

```go
type PGBackRestArchive struct {
    // ... existing fields ...

    // DR drill configuration
    DRDrill *DRDrillConfig `json:"drDrill,omitempty"`
}

type DRDrillConfig struct {
    // Enable automated DR drills
    Enabled bool `json:"enabled"`

    // Schedule for DR drills
    Schedule string `json:"schedule"` // Cron format

    // Target namespace for drill
    TargetNamespace string `json:"targetNamespace"`

    // Restore options
    RestoreOptions *RestoreOptions `json:"restoreOptions,omitempty"`

    // Validation queries
    ValidationQueries []ValidationQuery `json:"validationQueries,omitempty"`

    // Cleanup after drill
    CleanupAfterDrill bool `json:"cleanupAfterDrill"` // Default: true

    // Timeout for drill
    Timeout *metav1.Duration `json:"timeout,omitempty"`

    // Notification configuration
    Notifications *NotificationConfig `json:"notifications,omitempty"`
}

type ValidationQuery struct {
    // Database to connect to
    Database string `json:"database"`

    // Query to execute
    Query string `json:"query"`

    // Expected result (optional)
    ExpectedResult string `json:"expectedResult,omitempty"`
}

type NotificationConfig struct {
    // Webhook URL for notifications
    WebhookURL string `json:"webhookURL,omitempty"`

    // Slack webhook
    SlackWebhook string `json:"slackWebhook,omitempty"`

    // Email configuration
    Email *EmailConfig `json:"email,omitempty"`
}
```

#### Implementation Steps

1. **CronJob Creation**
   - Create CronJob that triggers DR drill
   - Job creates temporary PostgresCluster from backup

2. **Restore Execution**
   - Deploy cluster in target namespace
   - Restore from latest backup (or specified backup)
   - Wait for cluster to be ready

3. **Validation**
   - Execute validation queries
   - Check data integrity
   - Verify cluster is fully functional
   - Measure restore time (RTO)

4. **Reporting**
   - Record drill results in custom resource status
   - Create Kubernetes events
   - Send notifications (Slack, email, webhook)
   - Update metrics

5. **Cleanup**
   - Delete drill cluster
   - Remove temporary resources
   - Preserve logs for analysis

#### Workflow Diagram

```
┌─────────────────┐
│  Scheduled      │
│  CronJob        │
└────────┬────────┘
         │
         v
┌─────────────────┐
│  Create Temp    │
│  Namespace      │
└────────┬────────┘
         │
         v
┌─────────────────┐
│  Deploy         │
│  PostgresCluster│
│  with Restore   │
└────────┬────────┘
         │
         v
┌─────────────────┐
│  Wait for       │
│  Ready          │
└────────┬────────┘
         │
         v
┌─────────────────┐
│  Run Validation │
│  Queries        │
└────────┬────────┘
         │
         v
┌─────────────────┐
│  Measure RTO    │
│  Record Metrics │
└────────┬────────┘
         │
         v
┌─────────────────┐
│  Send           │
│  Notifications  │
└────────┬────────┘
         │
         v
┌─────────────────┐
│  Cleanup        │
│  Resources      │
└─────────────────┘
```

#### Files to Modify/Create
- `pkg/apis/postgres-operator.crunchydata.com/v1beta1/pgbackrest_types.go`
- `internal/pgbackrest/dr_drill.go` (new)
- `internal/controller/postgrescluster/dr_drill.go` (new)
- `internal/notifications/sender.go` (new)

---

## 6. Failover Time Optimization

### Overview
Optimize failover time to achieve sub-30-second recovery times.

### Goals
- Reduce mean time to recovery (MTTR)
- Minimize application downtime
- Maintain data consistency

### Current Baseline
- Typical failover: 30-60 seconds
- Components:
  - Detection: 10-20s (Patroni health checks)
  - Election: 5-10s (DCS coordination)
  - Promotion: 5-10s (pg_ctl promote)
  - Application reconnect: 5-20s

### Optimization Strategies

#### 1. Faster Failure Detection

**Current:**
```yaml
patroni:
  dynamicConfiguration:
    ttl: 30
    loop_wait: 10
    retry_timeout: 10
```

**Optimized:**
```yaml
patroni:
  dynamicConfiguration:
    ttl: 15  # Faster TTL
    loop_wait: 5  # More frequent checks
    retry_timeout: 5  # Faster timeout
    check_timeline: true
```

**Tradeoffs:**
- More frequent health checks increase CPU usage
- Lower TTL reduces split-brain risk but increases sensitivity

#### 2. Pre-promoted Standby

**Concept:**
- Keep one replica in "hot standby" mode
- Replica continuously replays WAL
- On failover, replica is already caught up

**Implementation:**
```yaml
patroni:
  dynamicConfiguration:
    synchronous_mode: false
    postgresql:
      parameters:
        synchronous_commit: "remote_write"  # Fast, safe
```

#### 3. Connection Pooler Configuration

**pgBouncer Optimization:**
```yaml
proxy:
  pgBouncer:
    config:
      global:
        server_check_delay: 10  # Faster backend health checks
        server_check_query: "SELECT 1"
        server_connect_timeout: 5
        query_wait_timeout: 60
```

#### 4. Application-Level Optimizations

**Recommendations for Applications:**
- Use connection pooling
- Implement retry logic with exponential backoff
- Set appropriate connection timeouts
- Use read replica for read queries

#### 5. DNS/Service Configuration

**Headless Service for Direct Pod Access:**
```yaml
kind: Service
spec:
  clusterIP: None  # Headless service
  publishNotReadyAddresses: false
```

**Benefits:**
- Clients connect directly to pods
- No kube-proxy latency
- Faster failover detection

#### Implementation Plan

**Phase 1: Patroni Tuning**
1. Add configuration API for advanced Patroni parameters
2. Provide presets (conservative, balanced, aggressive)
3. Document tradeoffs

**Phase 2: Monitoring**
1. Add metrics for failover time
2. Break down by component (detection, election, promotion)
3. Create dashboard for failover analysis

**Phase 3: Application Best Practices**
1. Document optimal client configuration
2. Provide sample connection code
3. Create testing tools for failover scenarios

**Phase 4: Testing**
1. Automated failover tests
2. Chaos engineering (pod deletion, network partition)
3. Load testing during failover
4. Document actual vs target times

#### Files to Modify/Create
- `pkg/apis/postgres-operator.crunchydata.com/v1beta1/patroni_types.go`
- `internal/patroni/failover.go` (new)
- `internal/controller/postgrescluster/failover_metrics.go` (new)
- `docs/FAILOVER_OPTIMIZATION.md` (new)

---

## 7. Interactive Cluster Creation Wizard

### Overview
CLI tool that guides users through creating a PostgresCluster with best practices.

### Goals
- Simplify cluster creation for new users
- Ensure best practices are followed
- Generate production-ready configurations

### Design

#### User Experience

```bash
$ pgo create cluster

╔══════════════════════════════════════════════════════╗
║      PostgreSQL Cluster Creation Wizard              ║
╚══════════════════════════════════════════════════════╝

This wizard will help you create a production-ready PostgreSQL cluster.

📋 Basic Information
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
? Cluster name: production-db
? Namespace: [default] production
? PostgreSQL version: [16] 16

🔧 Instance Configuration
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
? Number of replicas: [2] 3
? CPU request per instance: [1000m] 2000m
? Memory request per instance: [2Gi] 4Gi
? Storage size per instance: [20Gi] 100Gi
? Use separate WAL volume? [Y/n] y
? WAL volume size: [5Gi] 10Gi

💾 Backup Configuration
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
? Enable backups? [Y/n] y
? Backup repository type: [1. S3, 2. GCS, 3. Azure, 4. PVC] 1
? S3 bucket name: my-backups
? S3 region: us-east-1
? S3 endpoint: [s3.amazonaws.com]
? Backup schedule (full): [0 1 * * 0] 0 2 * * *
? Backup schedule (differential): [0 1 * * 1-6]
? Retention (days): [14] 30

🔒 Security Configuration
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
? Create application user? [Y/n] y
? Application username: myapp
? Application database: [myapp]

🔌 Connection Pooling
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
? Enable pgBouncer? [Y/n] y
? Number of pgBouncer replicas: [2] 2
? Pool mode: [1. session, 2. transaction, 3. statement] 2
? Max client connections: [1000]

📊 Monitoring
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
? Enable Prometheus monitoring? [Y/n] y

✅ Configuration Summary
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Cluster: production-db
Instances: 3 × (2 CPU, 4Gi RAM, 100Gi storage)
Backups: S3 (my-backups), daily full, 30-day retention
Connection pooling: pgBouncer (2 replicas, transaction mode)
Estimated cost: $XXX/month (varies by cloud provider)

? Create this cluster? [Y/n] y

✨ Creating cluster...
✓ Validating configuration
✓ Creating secrets
✓ Creating PostgresCluster resource
✓ Waiting for cluster to be ready... (this may take a few minutes)

🎉 Cluster created successfully!

Connection information:
  Host: production-db-primary.production.svc
  Port: 5432
  Database: myapp
  User: myapp
  Password: (stored in secret production-db-pguser-myapp)

Get password:
  kubectl get secret production-db-pguser-myapp -n production -o jsonpath='{.data.password}' | base64 -d

Connect:
  kubectl port-forward -n production svc/production-db-primary 5432:5432
  psql "host=localhost port=5432 dbname=myapp user=myapp sslmode=require"

Next steps:
  - Configure backups: kubectl edit postgrescluster production-db -n production
  - Set up monitoring: See docs/MONITORING.md
  - Review security: See docs/SECURITY.md
```

#### Implementation

**Technology Stack:**
- Language: Go
- CLI Framework: cobra + survey (interactive prompts)
- YAML generation: gopkg.in/yaml.v3
- Validation: controller-runtime validation

**File Structure:**
```
cmd/pgo/
├── create/
│   ├── cluster.go         # Main wizard logic
│   ├── prompts.go         # Interactive prompts
│   ├── templates.go       # Configuration templates
│   └── validation.go      # Input validation
├── get/
│   └── clusters.go
└── main.go
```

#### Features

1. **Presets**
   - Development (single instance, minimal resources)
   - Staging (2 replicas, moderate resources)
   - Production (3 replicas, HA, backups)

2. **Smart Defaults**
   - Calculate resource recommendations based on workload
   - Suggest storage size based on expected data volume
   - Recommend backup retention based on compliance needs

3. **Validation**
   - Check cluster name uniqueness
   - Validate resource availability
   - Verify storage class exists
   - Test cloud credentials (if provided)

4. **Output Options**
   - Apply directly to cluster
   - Save to YAML file
   - Print to stdout
   - Git-friendly format (with comments)

#### Files to Create
- `cmd/pgo/create/cluster.go`
- `cmd/pgo/create/prompts.go`
- `cmd/pgo/create/templates.go`
- `cmd/pgo/create/validation.go`
- `internal/wizard/config.go`

---

## 8-13. Additional Implementation Plans

Due to space constraints, abbreviated plans for remaining features:

### 8. Query Performance Insights
- Add pg_stat_statements monitoring
- Create performance dashboard
- Identify slow queries automatically
- Provide optimization recommendations

### 9. Connection Pool Analytics
- Enhanced pgBouncer metrics
- Pool utilization trends
- Connection queue analysis
- Auto-tuning recommendations

### 10. Backup Encryption at Rest
- Integrate with pgBackRest cipher support
- Key management (KMS, Vault)
- Compliance reporting

### 11. Cross-Region Backup Replication
- Multi-repository coordination
- Async replication to secondary region
- Failover to backup region

### 12. Configuration Validation Framework
- Pre-apply validation
- Dry-run mode
- Configuration drift detection
- Best practices checker

### 13. Auto-Scaling Read Replicas
- HPA integration
- Metrics-based scaling (CPU, connections, lag)
- Scale-up/down policies
- Load balancing integration

---

## Implementation Prioritization

### Phase 1 (Q1)
1. Backup Verification Automation
2. Enhanced Backup Metrics
3. Troubleshooting Diagnostics Tool ✅

### Phase 2 (Q2)
4. Failover Time Optimization
5. Configuration Validation Framework
6. Query Performance Insights

### Phase 3 (Q3)
7. Automated Secrets Rotation
8. Connection Pool Analytics
9. DR Drill Automation

### Phase 4 (Q4)
10. Interactive Cluster Creation Wizard
11. Auto-Scaling Read Replicas
12. FIPS Mode Support

### Future Releases
13. Backup Encryption at Rest
14. Cross-Region Backup Replication

---

## Contributing

To contribute to any of these implementations:

1. Review the relevant implementation plan
2. Check GitHub issues for related work
3. Discuss design decisions in issue or Discord
4. Follow development guidelines in CONTRIBUTING.md
5. Submit PR with tests and documentation

## References

- [PGO Architecture](../README.md)
- [API Documentation](../pkg/apis/postgres-operator.crunchydata.com/README.md)
- [Testing Strategy](../testing/README.md)
- [Contributing Guidelines](../CONTRIBUTING.md)
