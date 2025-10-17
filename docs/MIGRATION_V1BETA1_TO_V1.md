<!--
# Copyright 2025 Crunchy Data Solutions, Inc.
#
# SPDX-License-Identifier: Apache-2.0
-->

# Migration Guide: v1beta1 to v1 API

This guide helps you migrate PostgresCluster resources from `v1beta1` to `v1` API version.

## Overview

PGO (Postgres Operator) supports two API versions for the PostgresCluster custom resource:

- **v1beta1**: Compatible with Kubernetes 1.30+, OpenShift 4.14+
- **v1**: Requires Kubernetes 1.30+, OpenShift 4.17+ (uses advanced CEL validation)

Both versions are served simultaneously, and you can transition between them incrementally.

## Prerequisites

Before migrating to v1, ensure:

1. **Kubernetes Version**: Kubernetes 1.30+ or OpenShift 4.17+
2. **Validation Ratcheting**: Enabled by default in Kubernetes 1.30+ (CRDValidationRatcheting feature gate)
3. **Backup**: Always backup your PostgresCluster manifests before migration

## Key Differences Between v1beta1 and v1

### 1. Removed Fields

#### `spec.userInterface` (Removed in v1)

**v1beta1:**
```yaml
apiVersion: postgres-operator.crunchydata.com/v1beta1
kind: PostgresCluster
metadata:
  name: hippo
spec:
  userInterface:
    pgAdmin:
      dataVolumeClaimSpec:
        accessModes: [ReadWriteOnce]
        resources:
          requests:
            storage: 1Gi
```

**v1 Migration:** Use standalone PGAdmin CRD instead
```yaml
apiVersion: postgres-operator.crunchydata.com/v1beta1
kind: PGAdmin
metadata:
  name: hippo-pgadmin
spec:
  dataVolumeClaimSpec:
    accessModes: [ReadWriteOnce]
    resources:
      requests:
        storage: 1Gi
  serverGroups:
    - name: "Hippo Cluster"
      postgresClusterSelector:
        matchLabels:
          postgres-operator.crunchydata.com/cluster: hippo
```

### 2. Enhanced Validation

v1 includes stricter CEL validation rules that provide better error messages and prevent misconfigurations:

- **Log path validation**: pgBackRest log paths must be in additional volumes
- **Cross-field validation**: More sophisticated validation of related fields
- **Immutability checks**: Certain fields have stronger immutability guarantees

### 3. Improved OpenAPI Schema

v1 leverages newer Kubernetes CRD features for better schema validation without CEL overhead.

## Migration Steps

### Step 1: Check Compatibility

Before migrating, test your cluster definition with a dry-run:

```bash
# Get your existing v1beta1 cluster
kubectl get postgrescluster.v1beta1.postgres-operator.crunchydata.com hippo -o yaml > hippo-v1beta1.yaml

# Edit the file and change apiVersion to v1
sed 's|postgres-operator.crunchydata.com/v1beta1|postgres-operator.crunchydata.com/v1|' hippo-v1beta1.yaml > hippo-v1.yaml

# Test with dry-run
kubectl apply -f hippo-v1.yaml --dry-run=server
```

### Step 2: Address Validation Warnings

If you see warnings, address them before applying:

#### Warning: `spec.userInterface` should be null

**Action**: Remove `spec.userInterface` and create a standalone PGAdmin resource

```bash
# Extract userInterface configuration
yq eval '.spec.userInterface' hippo-v1beta1.yaml

# Create PGAdmin CR (see example above)
# Then remove userInterface from cluster spec
yq eval 'del(.spec.userInterface)' hippo-v1.yaml > hippo-v1-cleaned.yaml
```

#### Warning: pgBackRest log path not in additional volumes

**v1beta1 (may warn in v1):**
```yaml
spec:
  backups:
    pgbackrest:
      repoHost:
        log:
          path: /custom/path/logs
```

**v1 compliant:**
```yaml
spec:
  backups:
    pgbackrest:
      repoHost:
        log:
          path: /volumes/pgbackrest-log/logs
        volumes:
          additional:
            - name: pgbackrest-log
              volumeClaimSpec:
                accessModes: [ReadWriteOnce]
                resources:
                  requests:
                    storage: 1Gi
```

### Step 3: Apply the v1 Configuration

Once dry-run succeeds without warnings:

```bash
kubectl apply -f hippo-v1.yaml
```

### Step 4: Verify the Migration

Confirm the cluster is using v1:

```bash
# Check that the apiVersion is v1
kubectl get postgrescluster hippo -o yaml | grep apiVersion

# Verify cluster is healthy
kubectl get postgrescluster hippo

# Check cluster status
kubectl describe postgrescluster hippo
```

## Working with Multiple Versions

### Retrieving Specific Versions

By default, kubectl uses the version with highest priority (v1):

```bash
# Returns v1 representation (default)
kubectl get postgrescluster hippo -o yaml

# Explicitly request v1beta1
kubectl get postgrescluster.v1beta1.postgres-operator.crunchydata.com hippo -o yaml

# Explicitly request v1
kubectl get postgrescluster.v1.postgres-operator.crunchydata.com hippo -o yaml
```

### Getting Field Explanations

```bash
# Default (v1) field explanations
kubectl explain postgrescluster.spec.backups

# v1beta1 field explanations
kubectl explain postgrescluster.spec.backups --api-version=postgres-operator.crunchydata.com/v1beta1

# v1 field explanations
kubectl explain postgrescluster.spec.backups --api-version=postgres-operator.crunchydata.com/v1
```

## Validation Ratcheting

Kubernetes 1.30+ includes [validation ratcheting](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions#validation-ratcheting), which allows:

- **Gradual migration**: You can change `apiVersion` even if not all fields are valid in the new version
- **Unchanged fields pass validation**: Invalid fields that you don't modify are tolerated during updates
- **New fields must be valid**: Any newly added or modified fields must satisfy v1 validation

### Example

If your v1beta1 cluster has `spec.userInterface`:

```yaml
apiVersion: postgres-operator.crunchydata.com/v1beta1
kind: PostgresCluster
metadata:
  name: hippo
spec:
  userInterface:
    pgAdmin:
      # ...
  instances:
    - replicas: 2
```

You can change to v1 and update `replicas` **without** removing `userInterface` first:

```yaml
apiVersion: postgres-operator.crunchydata.com/v1  # Changed version
kind: PostgresCluster
metadata:
  name: hippo
spec:
  userInterface:   # Still here (warns but allowed due to ratcheting)
    pgAdmin:
      # ...
  instances:
    - replicas: 3  # Modified field (must be valid in v1)
```

This generates a warning but succeeds. Eventually, you should remove deprecated fields.

## Migration Checklist

- [ ] Verify Kubernetes version is 1.30+ (or OpenShift 4.17+)
- [ ] Backup all PostgresCluster manifests
- [ ] Review [key differences](#key-differences-between-v1beta1-and-v1)
- [ ] Test migration with `--dry-run=server`
- [ ] Address all validation errors
- [ ] (Optional) Address validation warnings
- [ ] Apply v1 configuration
- [ ] Verify cluster health after migration
- [ ] Update CI/CD pipelines to use v1 apiVersion
- [ ] Update documentation and runbooks

## Field-by-Field Changes

### Fields Removed in v1

| Field | Replacement | Migration Steps |
|-------|------------|-----------------|
| `spec.userInterface` | Standalone `PGAdmin` CRD | 1. Create PGAdmin CR<br/>2. Remove `spec.userInterface`<br/>3. Verify pgAdmin connectivity |

### Fields with Enhanced Validation in v1

| Field | v1beta1 Behavior | v1 Behavior | Migration Impact |
|-------|------------------|-------------|------------------|
| `spec.backups.pgbackrest.*.log.path` | Any path accepted | Must be in `/volumes/*` | Add corresponding volume to `volumes.additional` |
| `spec.backups.pgbackrest.global["log-path"]` | Allowed | Forbidden | Remove, use `log.path` fields instead |
| Various cross-field validations | Basic checks | Advanced CEL rules | Fix logical inconsistencies |

## Troubleshooting

### Error: Field is immutable

**Symptom:**
```
Error: field is immutable
```

**Solution:** Some fields cannot be changed. You may need to create a new cluster and migrate data.

### Error: Unknown field "userInterface"

**Symptom:**
```
Error: ValidationError(PostgresCluster.spec): unknown field "userInterface"
```

**Cause:** You're using a strict validation tool that doesn't support validation ratcheting.

**Solution:** Remove the deprecated field before applying.

### Warning: Field should be null

**Symptom:**
```
Warning: spec.userInterface: should be null
```

**Cause:** Validation ratcheting allows the field but warns it's deprecated.

**Solution:** This is safe to ignore temporarily, but remove the field eventually:

```bash
kubectl patch postgrescluster hippo --type=json \
  -p='[{"op": "remove", "path": "/spec/userInterface"}]'
```

### Tools (k9s, Lens) Show v1 When I Want v1beta1

**Symptom:** GUI tools always show v1 representation

**Cause:** Kubernetes version sorting prioritizes v1 over v1beta1

**Solution:** Use kubectl with explicit version:
```bash
kubectl get postgrescluster.v1beta1.postgres-operator.crunchydata.com hippo -o yaml
```

## Best Practices

1. **Migrate during maintenance window**: While changing `apiVersion` alone doesn't restart PostgreSQL, address field changes that might require reconciliation

2. **Test in non-production first**: Practice migration in dev/staging environments

3. **Automated validation**: Add to CI/CD:
   ```bash
   kubectl apply -f cluster.yaml --dry-run=server
   ```

4. **Keep v1beta1 manifests**: Retain original v1beta1 files as reference during migration period

5. **Monitor after migration**: Watch cluster events and operator logs:
   ```bash
   kubectl events --for postgrescluster/hippo --watch
   kubectl logs -n postgres-operator deployment/pgo -f
   ```

6. **Deprecation timeline**: Plan to complete migration before v1beta1 is removed in a future PGO release

## Additional Resources

- [CRD Versioning in Kubernetes](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning/)
- [Validation Ratcheting](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions#validation-ratcheting)
- [PGO API Documentation](../pkg/apis/postgres-operator.crunchydata.com/README.md)
- [PGO Validation Documentation](../pkg/apis/postgres-operator.crunchydata.com/validation.md)

## Support

If you encounter issues during migration:

- Check [PGO GitHub Issues](https://github.com/CrunchyData/postgres-operator/issues)
- Join [PGO Discord Community](https://discord.gg/crunchydata)
- Review operator logs for detailed error messages
- Use `--dry-run=server` to validate changes before applying
