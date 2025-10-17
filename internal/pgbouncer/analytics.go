// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbouncer

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/crunchydata/postgres-operator/internal/logging"
)

// Connection pool metrics
var (
	poolActiveConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_pgbouncer_active_connections",
			Help: "Number of active connections in pool",
		},
		[]string{"cluster", "namespace", "database", "user"},
	)

	poolWaitingClients = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_pgbouncer_waiting_clients",
			Help: "Number of clients waiting for a connection",
		},
		[]string{"cluster", "namespace", "database"},
	)

	poolUtilization = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_pgbouncer_pool_utilization",
			Help: "Connection pool utilization ratio (0-1)",
		},
		[]string{"cluster", "namespace", "database"},
	)

	poolConnectionWaitTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_pgbouncer_connection_wait_seconds",
			Help:    "Time clients wait for a connection",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
		},
		[]string{"cluster", "namespace", "database"},
	)

	poolQueriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_pgbouncer_queries_total",
			Help: "Total number of queries through pgBouncer",
		},
		[]string{"cluster", "namespace", "database"},
	)

	poolBytesReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_pgbouncer_bytes_received_total",
			Help: "Total bytes received by pgBouncer",
		},
		[]string{"cluster", "namespace", "database"},
	)

	poolBytesSent = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_pgbouncer_bytes_sent_total",
			Help: "Total bytes sent by pgBouncer",
		},
		[]string{"cluster", "namespace", "database"},
	)

	poolTransactionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_pgbouncer_transactions_total",
			Help: "Total number of transactions",
		},
		[]string{"cluster", "namespace", "database"},
	)

	poolMaxWaitTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_pgbouncer_max_wait_seconds",
			Help: "Maximum time a client waited for connection",
		},
		[]string{"cluster", "namespace", "database"},
	)
)

func init() {
	prometheus.MustRegister(
		poolActiveConnections,
		poolWaitingClients,
		poolUtilization,
		poolConnectionWaitTime,
		poolQueriesTotal,
		poolBytesReceived,
		poolBytesSent,
		poolTransactionsTotal,
		poolMaxWaitTime,
	)
}

// PoolAnalyticsConfig configures connection pool monitoring
type PoolAnalyticsConfig struct {
	// Enabled turns on pool analytics
	Enabled bool

	// CollectionInterval how often to collect statistics
	CollectionInterval time.Duration

	// UtilizationThreshold alert threshold for pool utilization
	UtilizationThreshold float64

	// WaitTimeThreshold alert threshold for wait time
	WaitTimeThreshold time.Duration

	// RecommendationEnabled generate auto-tuning recommendations
	RecommendationEnabled bool
}

// PoolStatistics contains pgBouncer pool statistics
type PoolStatistics struct {
	// Database database name
	Database string

	// User user name
	User string

	// Active active connections
	Active int

	// Waiting waiting clients
	Waiting int

	// MaxConnections maximum pool size
	MaxConnections int

	// Utilization pool utilization percentage
	Utilization float64

	// QueriesPerSecond query rate
	QueriesPerSecond float64

	// AverageQueryTime average query duration
	AverageQueryTime time.Duration

	// MaxWaitTime maximum client wait time
	MaxWaitTime time.Duration

	// TotalQueries total queries executed
	TotalQueries int64

	// TotalTransactions total transactions
	TotalTransactions int64

	// BytesReceived bytes received
	BytesReceived int64

	// BytesSent bytes sent
	BytesSent int64
}

// PoolAnalytics contains comprehensive pool analysis
type PoolAnalytics struct {
	// Timestamp when analytics were generated
	Timestamp time.Time

	// Pools statistics for each pool
	Pools []PoolStatistics

	// OverallUtilization overall pool utilization
	OverallUtilization float64

	// TotalWaitingClients total clients waiting
	TotalWaitingClients int

	// HighUtilizationPools pools with high utilization
	HighUtilizationPools []string

	// PoolsWithWaits pools experiencing waits
	PoolsWithWaits []string

	// Recommendations tuning suggestions
	Recommendations []PoolRecommendation

	// TrendAnalysis historical trend data
	TrendAnalysis *TrendAnalysis
}

// PoolRecommendation suggests pool configuration changes
type PoolRecommendation struct {
	// Severity priority level
	Severity string

	// Pool affected pool (database)
	Pool string

	// Issue description of problem
	Issue string

	// Recommendation suggested action
	Recommendation string

	// CurrentValue current configuration
	CurrentValue string

	// SuggestedValue recommended configuration
	SuggestedValue string

	// EstimatedImpact expected improvement
	EstimatedImpact string
}

// TrendAnalysis contains historical trend data
type TrendAnalysis struct {
	// UtilizationTrend utilization over time
	UtilizationTrend []TrendPoint

	// WaitTimeTrend wait time over time
	WaitTimeTrend []TrendPoint

	// ThroughputTrend queries per second over time
	ThroughputTrend []TrendPoint
}

// TrendPoint represents a single data point in a trend
type TrendPoint struct {
	Timestamp time.Time
	Value     float64
}

// CollectPoolStatistics collects pgBouncer statistics
func CollectPoolStatistics(ctx context.Context, db *sql.DB) ([]PoolStatistics, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	log := logging.FromContext(ctx)

	// Query pgBouncer SHOW POOLS
	poolQuery := `SHOW POOLS;`

	rows, err := db.QueryContext(ctx, poolQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query SHOW POOLS: %w", err)
	}
	defer rows.Close()

	pools := []PoolStatistics{}
	for rows.Next() {
		var p PoolStatistics
		var clActive, clWaiting, svActive, svIdle, svUsed, svTested, svLogin, maxWait int
		var mode string

		// SHOW POOLS columns: database, user, cl_active, cl_waiting, sv_active, sv_idle,
		// sv_used, sv_tested, sv_login, maxwait, pool_mode
		if err := rows.Scan(
			&p.Database,
			&p.User,
			&clActive,
			&clWaiting,
			&svActive,
			&svIdle,
			&svUsed,
			&svTested,
			&svLogin,
			&maxWait,
			&mode,
		); err != nil {
			log.Error(err, "failed to scan pool statistics")
			continue
		}

		p.Active = clActive
		p.Waiting = clWaiting
		p.MaxWaitTime = time.Duration(maxWait) * time.Second

		// Calculate total server connections
		totalServers := svActive + svIdle + svUsed + svTested + svLogin

		pools = append(pools, p)

		_ = totalServers // May use for metrics
	}

	// Query pgBouncer SHOW STATS for additional metrics
	statsQuery := `SHOW STATS;`
	statsRows, err := db.QueryContext(ctx, statsQuery)
	if err == nil {
		defer statsRows.Close()

		// Map to quickly look up pools
		poolMap := make(map[string]*PoolStatistics)
		for i := range pools {
			key := pools[i].Database
			poolMap[key] = &pools[i]
		}

		for statsRows.Next() {
			var database string
			var totalXactCount, totalQueryCount, totalReceived, totalSent int64
			var totalXactTime, totalQueryTime, totalWaitTime int64
			var avgXactCount, avgQueryCount, avgRecv, avgSent int64
			var avgXactTime, avgQueryTime, avgWaitTime int64

			// SHOW STATS columns: database, total_xact_count, total_query_count,
			// total_received, total_sent, total_xact_time, total_query_time, total_wait_time,
			// avg_xact_count, avg_query_count, avg_recv, avg_sent,
			// avg_xact_time, avg_query_time, avg_wait_time
			if err := statsRows.Scan(
				&database,
				&totalXactCount,
				&totalQueryCount,
				&totalReceived,
				&totalSent,
				&totalXactTime,
				&totalQueryTime,
				&totalWaitTime,
				&avgXactCount,
				&avgQueryCount,
				&avgRecv,
				&avgSent,
				&avgXactTime,
				&avgQueryTime,
				&avgWaitTime,
			); err != nil {
				continue
			}

			if pool, exists := poolMap[database]; exists {
				pool.TotalQueries = totalQueryCount
				pool.TotalTransactions = totalXactCount
				pool.BytesReceived = totalReceived
				pool.BytesSent = totalSent

				// Calculate average query time (microseconds to duration)
				if avgQueryTime > 0 {
					pool.AverageQueryTime = time.Duration(avgQueryTime) * time.Microsecond
				}
			}
		}
	}

	return pools, rows.Err()
}

// AnalyzePoolPerformance generates comprehensive pool analytics
func AnalyzePoolPerformance(
	ctx context.Context,
	db *sql.DB,
	config *PoolAnalyticsConfig,
) (*PoolAnalytics, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	analytics := &PoolAnalytics{
		Timestamp: time.Now(),
	}

	// Collect pool statistics
	pools, err := CollectPoolStatistics(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("failed to collect pool statistics: %w", err)
	}
	analytics.Pools = pools

	// Calculate overall metrics
	totalActive := 0
	totalWaiting := 0
	totalMax := 0

	for _, pool := range pools {
		totalActive += pool.Active
		totalWaiting += pool.Waiting

		// Check for high utilization
		if pool.Utilization > config.UtilizationThreshold {
			analytics.HighUtilizationPools = append(analytics.HighUtilizationPools, pool.Database)
		}

		// Check for waiting clients
		if pool.Waiting > 0 {
			analytics.PoolsWithWaits = append(analytics.PoolsWithWaits, pool.Database)
		}

		// Estimate max connections (would need SHOW CONFIG)
		totalMax += 25 // Default assumption
	}

	if totalMax > 0 {
		analytics.OverallUtilization = float64(totalActive) / float64(totalMax)
	}
	analytics.TotalWaitingClients = totalWaiting

	// Generate recommendations
	if config.RecommendationEnabled {
		analytics.Recommendations = GeneratePoolRecommendations(analytics, config)
	}

	return analytics, nil
}

// GeneratePoolRecommendations creates tuning recommendations
func GeneratePoolRecommendations(analytics *PoolAnalytics, config *PoolAnalyticsConfig) []PoolRecommendation {
	recommendations := []PoolRecommendation{}

	// Overall utilization recommendations
	if analytics.OverallUtilization > 0.80 {
		recommendations = append(recommendations, PoolRecommendation{
			Severity:        "high",
			Pool:            "all",
			Issue:           fmt.Sprintf("High overall pool utilization: %.1f%%", analytics.OverallUtilization*100),
			Recommendation:  "Increase pool sizes (default_pool_size or per-database pool_size)",
			CurrentValue:    "default_pool_size = 25 (assumed)",
			SuggestedValue:  "default_pool_size = 50 or higher",
			EstimatedImpact: "Reduce connection waits and improve application response time",
		})
	}

	// Per-pool recommendations
	for _, pool := range analytics.Pools {
		// High utilization
		if pool.Utilization > 0.90 {
			recommendations = append(recommendations, PoolRecommendation{
				Severity:        "high",
				Pool:            pool.Database,
				Issue:           fmt.Sprintf("Pool '%s' is at %.1f%% utilization", pool.Database, pool.Utilization*100),
				Recommendation:  fmt.Sprintf("Increase pool size for database '%s'", pool.Database),
				CurrentValue:    fmt.Sprintf("~%d connections", pool.MaxConnections),
				SuggestedValue:  fmt.Sprintf("~%d connections", int(float64(pool.MaxConnections)*1.5)),
				EstimatedImpact: "Eliminate connection queuing for this database",
			})
		}

		// Waiting clients
		if pool.Waiting > 5 {
			recommendations = append(recommendations, PoolRecommendation{
				Severity:        "high",
				Pool:            pool.Database,
				Issue:           fmt.Sprintf("Pool '%s' has %d waiting clients", pool.Database, pool.Waiting),
				Recommendation:  "Increase pool size or investigate slow queries",
				CurrentValue:    fmt.Sprintf("%d connections, %d waiting", pool.Active, pool.Waiting),
				SuggestedValue:  "Increase pool size by 20-50%",
				EstimatedImpact: "Reduce wait time from seconds to milliseconds",
			})
		}

		// Long wait times
		if pool.MaxWaitTime > 5*time.Second {
			recommendations = append(recommendations, PoolRecommendation{
				Severity:        "high",
				Pool:            pool.Database,
				Issue:           fmt.Sprintf("Pool '%s' has long wait times: %.1fs", pool.Database, pool.MaxWaitTime.Seconds()),
				Recommendation:  "Increase pool size or optimize query performance",
				CurrentValue:    fmt.Sprintf("max_wait = %.1fs", pool.MaxWaitTime.Seconds()),
				SuggestedValue:  "Target max_wait < 1s",
				EstimatedImpact: "Improve application responsiveness",
			})
		}

		// Low utilization (overprovisioned)
		if pool.Utilization < 0.20 && pool.MaxConnections > 10 {
			recommendations = append(recommendations, PoolRecommendation{
				Severity:        "low",
				Pool:            pool.Database,
				Issue:           fmt.Sprintf("Pool '%s' is underutilized: %.1f%%", pool.Database, pool.Utilization*100),
				Recommendation:  "Consider reducing pool size to free up resources",
				CurrentValue:    fmt.Sprintf("~%d connections", pool.MaxConnections),
				SuggestedValue:  fmt.Sprintf("~%d connections", int(float64(pool.MaxConnections)*0.6)),
				EstimatedImpact: "Free up memory and PostgreSQL backend resources",
			})
		}
	}

	// General recommendations
	if analytics.TotalWaitingClients == 0 && analytics.OverallUtilization < 0.50 {
		recommendations = append(recommendations, PoolRecommendation{
			Severity:        "info",
			Pool:            "all",
			Issue:           "Connection pooling is working well",
			Recommendation:  "Current configuration is appropriate. Continue monitoring.",
			CurrentValue:    fmt.Sprintf("Utilization: %.1f%%", analytics.OverallUtilization*100),
			SuggestedValue:  "No changes needed",
			EstimatedImpact: "N/A",
		})
	}

	return recommendations
}

// CalculateOptimalPoolSize suggests optimal pool configuration
func CalculateOptimalPoolSize(
	avgQueries float64,
	avgQueryTime time.Duration,
	targetWaitTime time.Duration,
) int {
	// Using queuing theory (M/M/c model)
	// pool_size = (arrival_rate * service_time) / (1 - target_utilization)

	// Arrival rate in queries per second
	lambda := avgQueries

	// Service time in seconds
	mu := avgQueryTime.Seconds()

	// Target utilization (80% to leave headroom)
	targetUtil := 0.80

	// Calculate required servers
	optimalSize := int((lambda * mu) / targetUtil)

	// Minimum pool size
	if optimalSize < 10 {
		optimalSize = 10
	}

	// Maximum pool size (reasonable limit)
	if optimalSize > 200 {
		optimalSize = 200
	}

	return optimalSize
}

// GeneratePoolReport creates a detailed pool analytics report
func GeneratePoolReport(analytics *PoolAnalytics) string {
	report := fmt.Sprintf(`
Connection Pool Analytics Report
=================================

Timestamp: %s
Overall Utilization: %.1f%%
Total Waiting Clients: %d

Pool Statistics:
----------------
`,
		analytics.Timestamp.Format(time.RFC3339),
		analytics.OverallUtilization*100,
		analytics.TotalWaitingClients,
	)

	for _, pool := range analytics.Pools {
		report += fmt.Sprintf(`
Database: %s (User: %s)
  Active Connections: %d
  Waiting Clients: %d
  Utilization: %.1f%%
  Max Wait Time: %.2fs
  Total Queries: %d
  Total Transactions: %d
  Avg Query Time: %.2fms
`,
			pool.Database,
			pool.User,
			pool.Active,
			pool.Waiting,
			pool.Utilization*100,
			pool.MaxWaitTime.Seconds(),
			pool.TotalQueries,
			pool.TotalTransactions,
			pool.AverageQueryTime.Seconds()*1000,
		)
	}

	if len(analytics.HighUtilizationPools) > 0 {
		report += "\nHigh Utilization Pools:\n"
		for _, pool := range analytics.HighUtilizationPools {
			report += fmt.Sprintf("  - %s\n", pool)
		}
	}

	if len(analytics.PoolsWithWaits) > 0 {
		report += "\nPools With Waiting Clients:\n"
		for _, pool := range analytics.PoolsWithWaits {
			report += fmt.Sprintf("  - %s\n", pool)
		}
	}

	report += "\nRecommendations:\n----------------\n"
	if len(analytics.Recommendations) == 0 {
		report += "  No recommendations at this time.\n"
	} else {
		for i, rec := range analytics.Recommendations {
			report += fmt.Sprintf(`
%d. [%s] %s
   Pool: %s
   Issue: %s
   Recommendation: %s
   Current: %s
   Suggested: %s
   Impact: %s
`,
				i+1,
				rec.Severity,
				rec.Pool,
				rec.Pool,
				rec.Issue,
				rec.Recommendation,
				rec.CurrentValue,
				rec.SuggestedValue,
				rec.EstimatedImpact,
			)
		}
	}

	return report
}

// ExportMetrics exports pool statistics to Prometheus
func ExportMetrics(clusterName, namespace string, pools []PoolStatistics) {
	for _, pool := range pools {
		poolActiveConnections.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
			pool.User,
		).Set(float64(pool.Active))

		poolWaitingClients.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
		).Set(float64(pool.Waiting))

		poolUtilization.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
		).Set(pool.Utilization)

		poolMaxWaitTime.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
		).Set(pool.MaxWaitTime.Seconds())

		poolQueriesTotal.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
		).Add(float64(pool.TotalQueries))

		poolTransactionsTotal.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
		).Add(float64(pool.TotalTransactions))

		poolBytesReceived.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
		).Add(float64(pool.BytesReceived))

		poolBytesSent.WithLabelValues(
			clusterName,
			namespace,
			pool.Database,
		).Add(float64(pool.BytesSent))
	}
}
