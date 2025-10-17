#!/usr/bin/env bash
# Copyright 2025 Crunchy Data Solutions, Inc.
#
# SPDX-License-Identifier: Apache-2.0

# PGO Diagnostics Collection Tool
# This script collects diagnostic information for troubleshooting PostgreSQL clusters
# managed by the Postgres Operator (PGO).

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
NAMESPACE="${NAMESPACE:-default}"
CLUSTER_NAME=""
OUTPUT_DIR=""
INCLUDE_LOGS="${INCLUDE_LOGS:-true}"
INCLUDE_SECRETS="${INCLUDE_SECRETS:-false}"
LOG_LINES="${LOG_LINES:-1000}"
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-postgres-operator}"

# Usage information
usage() {
    cat << EOF
Usage: $0 -c CLUSTER_NAME [-n NAMESPACE] [-o OUTPUT_DIR] [OPTIONS]

Collect diagnostic information for PGO PostgreSQL clusters.

Required:
    -c, --cluster NAME          PostgreSQL cluster name

Options:
    -n, --namespace NAMESPACE   Kubernetes namespace (default: default)
    -o, --output DIR            Output directory (default: diagnostics-CLUSTER-TIMESTAMP)
    -l, --log-lines N           Number of log lines to collect (default: 1000)
    --no-logs                   Skip collecting pod logs
    --include-secrets           Include secret values (WARNING: sensitive data)
    --operator-namespace NS     Operator namespace (default: postgres-operator)
    -h, --help                  Show this help message

Examples:
    # Basic diagnostics collection
    $0 -c hippo

    # Collect from specific namespace
    $0 -c hippo -n production

    # Include secret values (be careful!)
    $0 -c hippo --include-secrets

    # Collect more log lines
    $0 -c hippo -l 5000

EOF
    exit 1
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -c|--cluster)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -l|--log-lines)
            LOG_LINES="$2"
            shift 2
            ;;
        --no-logs)
            INCLUDE_LOGS=false
            shift
            ;;
        --include-secrets)
            INCLUDE_SECRETS=true
            shift
            ;;
        --operator-namespace)
            OPERATOR_NAMESPACE="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo -e "${RED}Error: Unknown option $1${NC}"
            usage
            ;;
    esac
done

# Validate required arguments
if [[ -z "$CLUSTER_NAME" ]]; then
    echo -e "${RED}Error: Cluster name is required${NC}"
    usage
fi

# Set default output directory if not specified
if [[ -z "$OUTPUT_DIR" ]]; then
    TIMESTAMP=$(date +%Y%m%d-%H%M%S)
    OUTPUT_DIR="diagnostics-${CLUSTER_NAME}-${TIMESTAMP}"
fi

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Section header
section() {
    echo ""
    echo -e "${GREEN}═══════════════════════════════════════════${NC}"
    echo -e "${GREEN}  $1${NC}"
    echo -e "${GREEN}═══════════════════════════════════════════${NC}"
}

# Execute command and save to file
exec_and_save() {
    local cmd="$1"
    local output_file="$2"
    local description="${3:-}"

    if [[ -n "$description" ]]; then
        log_info "$description"
    fi

    if eval "$cmd" > "$OUTPUT_DIR/$output_file" 2>&1; then
        log_success "Saved to $output_file"
    else
        log_warning "Failed to collect: $output_file (this may be expected)"
    fi
}

# Check if cluster exists
check_cluster() {
    log_info "Checking if cluster '$CLUSTER_NAME' exists in namespace '$NAMESPACE'..."

    if ! kubectl get postgrescluster "$CLUSTER_NAME" -n "$NAMESPACE" &>/dev/null; then
        log_error "PostgresCluster '$CLUSTER_NAME' not found in namespace '$NAMESPACE'"
        exit 1
    fi

    log_success "Cluster found"
}

# Collect cluster definition
collect_cluster_definition() {
    section "Collecting Cluster Definition"

    exec_and_save \
        "kubectl get postgrescluster '$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "cluster.yaml" \
        "Collecting PostgresCluster definition"

    exec_and_save \
        "kubectl get postgrescluster '$CLUSTER_NAME' -n '$NAMESPACE' -o json | jq '.status'" \
        "cluster-status.json" \
        "Collecting cluster status"
}

# Collect pod information
collect_pod_info() {
    section "Collecting Pod Information"

    exec_and_save \
        "kubectl get pods -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o wide" \
        "pods.txt" \
        "Collecting pod list"

    exec_and_save \
        "kubectl get pods -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "pods.yaml" \
        "Collecting detailed pod information"

    # Describe each pod
    log_info "Collecting pod descriptions..."
    mkdir -p "$OUTPUT_DIR/pod-descriptions"

    kubectl get pods -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME" -n "$NAMESPACE" -o name | while read -r pod; do
        pod_name=$(basename "$pod")
        kubectl describe pod "$pod_name" -n "$NAMESPACE" > "$OUTPUT_DIR/pod-descriptions/${pod_name}.txt" 2>&1
    done
    log_success "Saved pod descriptions"
}

# Collect pod logs
collect_pod_logs() {
    if [[ "$INCLUDE_LOGS" != "true" ]]; then
        log_info "Skipping log collection (--no-logs specified)"
        return
    fi

    section "Collecting Pod Logs"

    mkdir -p "$OUTPUT_DIR/logs"

    kubectl get pods -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME" -n "$NAMESPACE" -o name | while read -r pod; do
        pod_name=$(basename "$pod")
        log_info "Collecting logs from $pod_name"

        # Get list of containers in the pod
        containers=$(kubectl get pod "$pod_name" -n "$NAMESPACE" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null || echo "")

        for container in $containers; do
            # Current logs
            kubectl logs "$pod_name" -n "$NAMESPACE" -c "$container" --tail="$LOG_LINES" \
                > "$OUTPUT_DIR/logs/${pod_name}-${container}.log" 2>&1 || true

            # Previous logs (if pod restarted)
            kubectl logs "$pod_name" -n "$NAMESPACE" -c "$container" --previous --tail="$LOG_LINES" \
                > "$OUTPUT_DIR/logs/${pod_name}-${container}-previous.log" 2>&1 || true
        done

        # Init container logs
        init_containers=$(kubectl get pod "$pod_name" -n "$NAMESPACE" -o jsonpath='{.spec.initContainers[*].name}' 2>/dev/null || echo "")

        for container in $init_containers; do
            kubectl logs "$pod_name" -n "$NAMESPACE" -c "$container" --tail="$LOG_LINES" \
                > "$OUTPUT_DIR/logs/${pod_name}-init-${container}.log" 2>&1 || true
        done
    done

    log_success "Saved pod logs"
}

# Collect events
collect_events() {
    section "Collecting Events"

    exec_and_save \
        "kubectl get events -n '$NAMESPACE' --sort-by='.lastTimestamp' | grep '$CLUSTER_NAME'" \
        "events.txt" \
        "Collecting cluster events"

    exec_and_save \
        "kubectl get events -n '$NAMESPACE' --sort-by='.lastTimestamp' -o yaml | grep -A 50 '$CLUSTER_NAME'" \
        "events-detailed.yaml" \
        "Collecting detailed events"
}

# Collect PVCs
collect_pvcs() {
    section "Collecting PVC Information"

    exec_and_save \
        "kubectl get pvc -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o wide" \
        "pvcs.txt" \
        "Collecting PVC list"

    exec_and_save \
        "kubectl get pvc -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "pvcs.yaml" \
        "Collecting detailed PVC information"
}

# Collect Services
collect_services() {
    section "Collecting Service Information"

    exec_and_save \
        "kubectl get svc -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o wide" \
        "services.txt" \
        "Collecting service list"

    exec_and_save \
        "kubectl get svc -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "services.yaml" \
        "Collecting detailed service information"

    exec_and_save \
        "kubectl get endpoints -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "endpoints.yaml" \
        "Collecting service endpoints"
}

# Collect ConfigMaps
collect_configmaps() {
    section "Collecting ConfigMap Information"

    exec_and_save \
        "kubectl get cm -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "configmaps.yaml" \
        "Collecting ConfigMaps"
}

# Collect Secrets (metadata only by default)
collect_secrets() {
    section "Collecting Secret Information"

    if [[ "$INCLUDE_SECRETS" == "true" ]]; then
        log_warning "Including secret values (--include-secrets specified)"
        exec_and_save \
            "kubectl get secrets -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
            "secrets-WITH-VALUES.yaml" \
            "Collecting secrets WITH VALUES"
    else
        log_info "Collecting secret metadata only (use --include-secrets to include values)"
        exec_and_save \
            "kubectl get secrets -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml | kubectl neat 2>/dev/null || kubectl get secrets -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o json | jq 'del(.items[].data, .items[].stringData)'" \
            "secrets-metadata.yaml" \
            "Collecting secret metadata"
    fi
}

# Collect Jobs
collect_jobs() {
    section "Collecting Job Information"

    exec_and_save \
        "kubectl get jobs -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o wide" \
        "jobs.txt" \
        "Collecting job list"

    exec_and_save \
        "kubectl get jobs -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "jobs.yaml" \
        "Collecting detailed job information"

    exec_and_save \
        "kubectl get cronjobs -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml" \
        "cronjobs.yaml" \
        "Collecting CronJobs"
}

# Collect Patroni status
collect_patroni_status() {
    section "Collecting Patroni Status"

    # Find a running instance pod
    instance_pod=$(kubectl get pods -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME",postgres-operator.crunchydata.com/instance -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

    if [[ -n "$instance_pod" ]]; then
        log_info "Collecting Patroni status from $instance_pod"

        exec_and_save \
            "kubectl exec '$instance_pod' -n '$NAMESPACE' -c database -- patronictl list 2>/dev/null || echo 'Failed to get Patroni status'" \
            "patroni-status.txt" \
            "Collecting Patroni cluster status"

        exec_and_save \
            "kubectl exec '$instance_pod' -n '$NAMESPACE' -c database -- patronictl show-config 2>/dev/null || echo 'Failed to get Patroni config'" \
            "patroni-config.yaml" \
            "Collecting Patroni configuration"
    else
        log_warning "No instance pods found, skipping Patroni status collection"
    fi
}

# Collect pgBackRest status
collect_pgbackrest_status() {
    section "Collecting pgBackRest Status"

    # Find a running instance pod
    instance_pod=$(kubectl get pods -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME",postgres-operator.crunchydata.com/instance -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

    if [[ -n "$instance_pod" ]]; then
        log_info "Collecting pgBackRest status from $instance_pod"

        exec_and_save \
            "kubectl exec '$instance_pod' -n '$NAMESPACE' -c database -- pgbackrest info 2>/dev/null || echo 'Failed to get pgBackRest info'" \
            "pgbackrest-info.txt" \
            "Collecting pgBackRest repository status"

        exec_and_save \
            "kubectl exec '$instance_pod' -n '$NAMESPACE' -c database -- pgbackrest check 2>/dev/null || echo 'Failed to run pgBackRest check'" \
            "pgbackrest-check.txt" \
            "Running pgBackRest check"
    else
        log_warning "No instance pods found, skipping pgBackRest status collection"
    fi
}

# Collect PostgreSQL status
collect_postgres_status() {
    section "Collecting PostgreSQL Status"

    # Find the primary pod
    primary_pod=$(kubectl get pods -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME",postgres-operator.crunchydata.com/role=master -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

    if [[ -n "$primary_pod" ]]; then
        log_info "Collecting PostgreSQL status from primary: $primary_pod"

        exec_and_save \
            "kubectl exec '$primary_pod' -n '$NAMESPACE' -c database -- psql -c 'SELECT version();' 2>/dev/null || echo 'Failed to query PostgreSQL version'" \
            "postgres-version.txt" \
            "Collecting PostgreSQL version"

        exec_and_save \
            "kubectl exec '$primary_pod' -n '$NAMESPACE' -c database -- psql -c 'SELECT name, setting, unit, context FROM pg_settings ORDER BY name;' 2>/dev/null || echo 'Failed to query settings'" \
            "postgres-settings.txt" \
            "Collecting PostgreSQL settings"

        exec_and_save \
            "kubectl exec '$primary_pod' -n '$NAMESPACE' -c database -- psql -c 'SELECT * FROM pg_stat_replication;' 2>/dev/null || echo 'No replication status'" \
            "postgres-replication.txt" \
            "Collecting replication status"

        exec_and_save \
            "kubectl exec '$primary_pod' -n '$NAMESPACE' -c database -- psql -c 'SELECT datname, pg_size_pretty(pg_database_size(datname)) FROM pg_database;' 2>/dev/null || echo 'Failed to query database sizes'" \
            "postgres-database-sizes.txt" \
            "Collecting database sizes"

        exec_and_save \
            "kubectl exec '$primary_pod' -n '$NAMESPACE' -c database -- psql -c 'SELECT slot_name, plugin, slot_type, database, active, restart_lsn FROM pg_replication_slots;' 2>/dev/null || echo 'No replication slots'" \
            "postgres-replication-slots.txt" \
            "Collecting replication slots"
    else
        log_warning "No primary pod found, skipping PostgreSQL status collection"
    fi
}

# Collect operator logs
collect_operator_logs() {
    section "Collecting Operator Logs"

    operator_pod=$(kubectl get pods -n "$OPERATOR_NAMESPACE" -l app.kubernetes.io/name=pgo -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

    if [[ -n "$operator_pod" ]]; then
        log_info "Collecting operator logs from $operator_pod"

        exec_and_save \
            "kubectl logs '$operator_pod' -n '$OPERATOR_NAMESPACE' --tail='$LOG_LINES' 2>/dev/null || echo 'Failed to get operator logs'" \
            "operator-logs.txt" \
            "Collecting operator logs"

        # Filter for cluster-specific logs
        if [[ -f "$OUTPUT_DIR/operator-logs.txt" ]]; then
            grep "$CLUSTER_NAME" "$OUTPUT_DIR/operator-logs.txt" > "$OUTPUT_DIR/operator-logs-cluster.txt" 2>/dev/null || true
        fi
    else
        log_warning "Operator pod not found in namespace $OPERATOR_NAMESPACE"
    fi
}

# Collect related CRDs
collect_related_crds() {
    section "Collecting Related Resources"

    # Check for PGAdmin
    exec_and_save \
        "kubectl get pgadmin -n '$NAMESPACE' -o yaml 2>/dev/null || echo 'No PGAdmin resources found'" \
        "pgadmin.yaml" \
        "Collecting PGAdmin resources"

    # Check for PGUpgrade
    exec_and_save \
        "kubectl get pgupgrade -l postgres-operator.crunchydata.com/cluster='$CLUSTER_NAME' -n '$NAMESPACE' -o yaml 2>/dev/null || echo 'No PGUpgrade resources found'" \
        "pgupgrade.yaml" \
        "Collecting PGUpgrade resources"
}

# Collect system information
collect_system_info() {
    section "Collecting System Information"

    exec_and_save \
        "kubectl version --short 2>/dev/null || kubectl version" \
        "kubectl-version.txt" \
        "Collecting kubectl version"

    exec_and_save \
        "kubectl get nodes -o wide" \
        "nodes.txt" \
        "Collecting node information"

    exec_and_save \
        "kubectl top nodes 2>/dev/null || echo 'Metrics server not available'" \
        "nodes-metrics.txt" \
        "Collecting node metrics"

    exec_and_save \
        "kubectl get storageclass" \
        "storageclasses.txt" \
        "Collecting storage classes"
}

# Create summary report
create_summary() {
    section "Creating Summary Report"

    cat > "$OUTPUT_DIR/SUMMARY.txt" << EOF
PGO Diagnostics Summary
========================

Collection Date: $(date)
Cluster Name: $CLUSTER_NAME
Namespace: $NAMESPACE
Operator Namespace: $OPERATOR_NAMESPACE

Files Collected:
$(find "$OUTPUT_DIR" -type f | wc -l) files

Cluster Status:
$(kubectl get postgrescluster "$CLUSTER_NAME" -n "$NAMESPACE" 2>/dev/null | tail -n +2 || echo "Failed to get cluster status")

Pods:
$(kubectl get pods -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME" -n "$NAMESPACE" 2>/dev/null | tail -n +2 || echo "No pods found")

Services:
$(kubectl get svc -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME" -n "$NAMESPACE" 2>/dev/null | tail -n +2 || echo "No services found")

PVCs:
$(kubectl get pvc -l postgres-operator.crunchydata.com/cluster="$CLUSTER_NAME" -n "$NAMESPACE" 2>/dev/null | tail -n +2 || echo "No PVCs found")

Next Steps:
1. Review the SUMMARY.txt file (this file)
2. Check pod logs in the logs/ directory
3. Review events.txt for cluster events
4. Check cluster.yaml for configuration
5. Review postgres-*.txt files for database status
6. Check operator-logs.txt for operator issues

For support, share the entire diagnostics directory: $OUTPUT_DIR
EOF

    log_success "Summary report created"
}

# Create tarball
create_archive() {
    section "Creating Archive"

    archive_name="${OUTPUT_DIR}.tar.gz"

    if tar -czf "$archive_name" "$OUTPUT_DIR" 2>/dev/null; then
        log_success "Created archive: $archive_name"

        # Calculate size
        size=$(du -h "$archive_name" | cut -f1)
        log_info "Archive size: $size"

        if [[ "$INCLUDE_SECRETS" == "true" ]]; then
            log_warning "Archive contains secret values! Handle with care."
        fi
    else
        log_warning "Failed to create archive"
    fi
}

# Main execution
main() {
    echo ""
    echo -e "${GREEN}╔════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║                                                    ║${NC}"
    echo -e "${GREEN}║        PGO Diagnostics Collection Tool            ║${NC}"
    echo -e "${GREEN}║                                                    ║${NC}"
    echo -e "${GREEN}╚════════════════════════════════════════════════════╝${NC}"
    echo ""

    log_info "Starting diagnostics collection for cluster: $CLUSTER_NAME"
    log_info "Namespace: $NAMESPACE"
    log_info "Output directory: $OUTPUT_DIR"
    echo ""

    # Check if cluster exists
    check_cluster

    # Collect all information
    collect_cluster_definition
    collect_pod_info
    collect_pod_logs
    collect_events
    collect_pvcs
    collect_services
    collect_configmaps
    collect_secrets
    collect_jobs
    collect_patroni_status
    collect_pgbackrest_status
    collect_postgres_status
    collect_operator_logs
    collect_related_crds
    collect_system_info
    create_summary
    create_archive

    # Final summary
    echo ""
    section "Collection Complete!"
    echo ""
    log_success "Diagnostics collected successfully!"
    log_info "Output directory: $OUTPUT_DIR"
    log_info "Archive: ${OUTPUT_DIR}.tar.gz"
    echo ""
    log_info "Review the SUMMARY.txt file for an overview"
    log_info "Share the entire archive when requesting support"
    echo ""

    if [[ "$INCLUDE_SECRETS" == "true" ]]; then
        echo -e "${RED}╔════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  WARNING: Archive contains sensitive data!        ║${NC}"
        echo -e "${RED}║  Do not share publicly. Use secure channels only. ║${NC}"
        echo -e "${RED}╚════════════════════════════════════════════════════╝${NC}"
        echo ""
    fi
}

# Run main function
main
