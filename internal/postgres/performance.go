// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/crunchydata/postgres-operator/internal/logging"
)

// Query performance metrics
var (
	queryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_query_duration_seconds",
			Help:    "Query execution duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~16s
		},
		[]string{"cluster", "namespace", "database", "user", "query_id"},
	)

	queryCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_query_calls_total",
			Help: "Total number of query executions",
		},
		[]string{"cluster", "namespace", "database", "user", "query_id"},
	)

	queryRowsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_query_rows_total",
			Help: "Total number of rows returned by queries",
		},
		[]string{"cluster", "namespace", "database", "user", "query_id"},
	)

	slowQueryTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_slow_query_total",
			Help: "Total number of slow queries",
		},
		[]string{"cluster", "namespace", "database", "threshold"},
	)

	cacheHitRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_cache_hit_ratio",
			Help: "PostgreSQL cache hit ratio (0-1)",
		},
		[]string{"cluster", "namespace", "database"},
	)

	indexUsageRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_index_usage_ratio",
			Help: "Index usage ratio for tables (0-1)",
		},
		[]string{"cluster", "namespace", "database", "schema", "table"},
	)
)

func init() {
	prometheus.MustRegister(
		queryDuration,
		queryCallsTotal,
		queryRowsTotal,
		slowQueryTotal,
		cacheHitRatio,
		indexUsageRatio,
	)
}

// QueryPerformanceConfig configures query performance monitoring
type QueryPerformanceConfig struct {
	// Enabled turns on query performance monitoring
	Enabled bool

	// SlowQueryThreshold defines what constitutes a slow query
	SlowQueryThreshold time.Duration

	// TopQueriesCount how many top queries to track
	TopQueriesCount int

	// CollectionInterval how often to collect statistics
	CollectionInterval time.Duration

	// NormalizeQueries whether to normalize query text
	NormalizeQueries bool

	// TrackIOTiming whether to track I/O timing
	TrackIOTiming bool

	// MaxQueryTextLength maximum length of query text to store
	MaxQueryTextLength int
}

// QueryStatistics represents statistics for a single query
type QueryStatistics struct {
	// QueryID unique identifier for the query
	QueryID string

	// Query normalized query text
	Query string

	// Database where query was executed
	Database string

	// User who executed the query
	User string

	// Calls total number of executions
	Calls int64

	// TotalTime total execution time
	TotalTime time.Duration

	// MinTime minimum execution time
	MinTime time.Duration

	// MaxTime maximum execution time
	MaxTime time.Duration

	// MeanTime average execution time
	MeanTime time.Duration

	// StddevTime standard deviation of execution time
	StddevTime time.Duration

	// Rows total rows returned/affected
	Rows int64

	// SharedBlksHit blocks read from shared buffer cache
	SharedBlksHit int64

	// SharedBlksRead blocks read from disk
	SharedBlksRead int64

	// SharedBlksDirtied blocks dirtied
	SharedBlksDirtied int64

	// SharedBlksWritten blocks written
	SharedBlksWritten int64

	// LocalBlksHit blocks read from local cache
	LocalBlksHit int64

	// LocalBlksRead blocks read from local disk
	LocalBlksRead int64

	// TempBlksRead temp blocks read
	TempBlksRead int64

	// TempBlksWritten temp blocks written
	TempBlksWritten int64

	// BlockReadTime time spent reading blocks
	BlockReadTime time.Duration

	// BlockWriteTime time spent writing blocks
	BlockWriteTime time.Duration
}

// PerformanceInsights contains comprehensive performance analysis
type PerformanceInsights struct {
	// Timestamp when insights were generated
	Timestamp time.Time

	// SlowQueries list of queries exceeding threshold
	SlowQueries []QueryStatistics

	// TopQueriesByTime queries with highest total time
	TopQueriesByTime []QueryStatistics

	// TopQueriesByCalls queries with most executions
	TopQueriesByCalls []QueryStatistics

	// TopQueriesByIOTime queries with highest I/O time
	TopQueriesByIOTime []QueryStatistics

	// CacheHitRatio overall cache hit ratio
	CacheHitRatio float64

	// Recommendations optimization suggestions
	Recommendations []PerformanceRecommendation

	// IndexAnalysis unused and missing indexes
	IndexAnalysis IndexAnalysis

	// TableStatistics table-level statistics
	TableStatistics []TableStatistics
}

// PerformanceRecommendation suggests query optimization
type PerformanceRecommendation struct {
	// Severity priority level (high, medium, low)
	Severity string

	// Category type of recommendation
	Category string

	// QueryID affected query (if applicable)
	QueryID string

	// Query text (if applicable)
	Query string

	// Issue description of the problem
	Issue string

	// Recommendation how to fix
	Recommendation string

	// EstimatedImpact expected improvement
	EstimatedImpact string
}

// IndexAnalysis contains index usage information
type IndexAnalysis struct {
	// UnusedIndexes indexes that are never used
	UnusedIndexes []IndexInfo

	// LowUsageIndexes indexes rarely used
	LowUsageIndexes []IndexInfo

	// MissingIndexes suggested indexes based on seq scans
	MissingIndexes []IndexSuggestion

	// DuplicateIndexes redundant indexes
	DuplicateIndexes []IndexInfo
}

// IndexInfo contains information about an index
type IndexInfo struct {
	// Schema schema name
	Schema string

	// Table table name
	Table string

	// Index index name
	Index string

	// Size index size in bytes
	Size int64

	// Scans number of index scans
	Scans int64

	// TuplesRead tuples read via index
	TuplesRead int64

	// TuplesFetched tuples fetched via index
	TuplesFetched int64
}

// IndexSuggestion recommends creating an index
type IndexSuggestion struct {
	// Schema schema name
	Schema string

	// Table table name
	Table string

	// Columns suggested columns for index
	Columns []string

	// Reason why index is recommended
	Reason string

	// SeqScans number of sequential scans
	SeqScans int64

	// EstimatedImprovement expected speedup
	EstimatedImprovement string
}

// TableStatistics contains table-level performance data
type TableStatistics struct {
	// Schema schema name
	Schema string

	// Table table name
	Table string

	// SeqScans sequential scans
	SeqScans int64

	// SeqTuplesRead tuples read via seq scans
	SeqTuplesRead int64

	// IndexScans index scans
	IndexScans int64

	// IndexTuplesRead tuples read via index scans
	IndexTuplesRead int64

	// InsertedTuples tuples inserted
	InsertedTuples int64

	// UpdatedTuples tuples updated
	UpdatedTuples int64

	// DeletedTuples tuples deleted
	DeletedTuples int64

	// LiveTuples current live tuples
	LiveTuples int64

	// DeadTuples current dead tuples
	DeadTuples int64

	// VacuumCount manual vacuum count
	VacuumCount int64

	// AutovacuumCount autovacuum count
	AutovacuumCount int64

	// LastVacuum last vacuum time
	LastVacuum *time.Time

	// LastAutovacuum last autovacuum time
	LastAutovacuum *time.Time
}

// CollectQueryStatistics collects pg_stat_statements data
func CollectQueryStatistics(
	ctx context.Context,
	db *sql.DB,
	config *QueryPerformanceConfig,
) ([]QueryStatistics, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	log := logging.FromContext(ctx)

	query := `
		SELECT
			queryid,
			query,
			calls,
			total_exec_time,
			min_exec_time,
			max_exec_time,
			mean_exec_time,
			stddev_exec_time,
			rows,
			shared_blks_hit,
			shared_blks_read,
			shared_blks_dirtied,
			shared_blks_written,
			local_blks_hit,
			local_blks_read,
			temp_blks_read,
			temp_blks_written,
			blk_read_time,
			blk_write_time
		FROM pg_stat_statements
		WHERE queryid IS NOT NULL
		ORDER BY total_exec_time DESC
		LIMIT $1;
	`

	rows, err := db.QueryContext(ctx, query, config.TopQueriesCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	stats := []QueryStatistics{}
	for rows.Next() {
		var s QueryStatistics
		var totalTimeMs, minTimeMs, maxTimeMs, meanTimeMs, stddevTimeMs float64
		var blkReadTimeMs, blkWriteTimeMs float64

		if err := rows.Scan(
			&s.QueryID,
			&s.Query,
			&s.Calls,
			&totalTimeMs,
			&minTimeMs,
			&maxTimeMs,
			&meanTimeMs,
			&stddevTimeMs,
			&s.Rows,
			&s.SharedBlksHit,
			&s.SharedBlksRead,
			&s.SharedBlksDirtied,
			&s.SharedBlksWritten,
			&s.LocalBlksHit,
			&s.LocalBlksRead,
			&s.TempBlksRead,
			&s.TempBlksWritten,
			&blkReadTimeMs,
			&blkWriteTimeMs,
		); err != nil {
			log.Error(err, "failed to scan query statistics")
			continue
		}

		// Convert milliseconds to durations
		s.TotalTime = time.Duration(totalTimeMs * float64(time.Millisecond))
		s.MinTime = time.Duration(minTimeMs * float64(time.Millisecond))
		s.MaxTime = time.Duration(maxTimeMs * float64(time.Millisecond))
		s.MeanTime = time.Duration(meanTimeMs * float64(time.Millisecond))
		s.StddevTime = time.Duration(stddevTimeMs * float64(time.Millisecond))
		s.BlockReadTime = time.Duration(blkReadTimeMs * float64(time.Millisecond))
		s.BlockWriteTime = time.Duration(blkWriteTimeMs * float64(time.Millisecond))

		stats = append(stats, s)
	}

	return stats, rows.Err()
}

// AnalyzePerformance generates comprehensive performance insights
func AnalyzePerformance(
	ctx context.Context,
	db *sql.DB,
	config *QueryPerformanceConfig,
) (*PerformanceInsights, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	insights := &PerformanceInsights{
		Timestamp: time.Now(),
	}

	// Collect query statistics
	queryStats, err := CollectQueryStatistics(ctx, db, config)
	if err != nil {
		return nil, fmt.Errorf("failed to collect query statistics: %w", err)
	}

	// Identify slow queries
	for _, stat := range queryStats {
		if stat.MeanTime > config.SlowQueryThreshold {
			insights.SlowQueries = append(insights.SlowQueries, stat)
		}
	}

	// Top queries by total time
	insights.TopQueriesByTime = queryStats[:min(10, len(queryStats))]

	// Calculate cache hit ratio
	cacheHit, err := CalculateCacheHitRatio(ctx, db)
	if err == nil {
		insights.CacheHitRatio = cacheHit
	}

	// Analyze indexes
	indexAnalysis, err := AnalyzeIndexes(ctx, db)
	if err == nil {
		insights.IndexAnalysis = *indexAnalysis
	}

	// Collect table statistics
	tableStats, err := CollectTableStatistics(ctx, db)
	if err == nil {
		insights.TableStatistics = tableStats
	}

	// Generate recommendations
	insights.Recommendations = GenerateRecommendations(insights, config)

	return insights, nil
}

// CalculateCacheHitRatio calculates the buffer cache hit ratio
func CalculateCacheHitRatio(ctx context.Context, db *sql.DB) (float64, error) {
	if db == nil {
		return 0, fmt.Errorf("database connection is nil")
	}
	query := `
		SELECT
			CASE
				WHEN (blks_hit + blks_read) = 0 THEN 1.0
				ELSE blks_hit::float / (blks_hit + blks_read)
			END as cache_hit_ratio
		FROM pg_stat_database
		WHERE datname = current_database();
	`

	var ratio float64
	if err := db.QueryRowContext(ctx, query).Scan(&ratio); err != nil {
		return 0, fmt.Errorf("failed to calculate cache hit ratio: %w", err)
	}

	return ratio, nil
}

// AnalyzeIndexes analyzes index usage and suggests improvements
func AnalyzeIndexes(ctx context.Context, db *sql.DB) (*IndexAnalysis, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	analysis := &IndexAnalysis{}

	// Find unused indexes
	unusedQuery := `
		SELECT
			schemaname,
			tablename,
			indexname,
			pg_relation_size(indexrelid) as index_size,
			idx_scan
		FROM pg_stat_user_indexes
		WHERE idx_scan = 0
		  AND schemaname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY pg_relation_size(indexrelid) DESC
		LIMIT 20;
	`

	rows, err := db.QueryContext(ctx, unusedQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query unused indexes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var idx IndexInfo
		if err := rows.Scan(&idx.Schema, &idx.Table, &idx.Index, &idx.Size, &idx.Scans); err != nil {
			continue
		}
		analysis.UnusedIndexes = append(analysis.UnusedIndexes, idx)
	}

	// Find tables with high sequential scan counts (missing indexes)
	missingQuery := `
		SELECT
			schemaname,
			tablename,
			seq_scan,
			seq_tup_read,
			idx_scan
		FROM pg_stat_user_tables
		WHERE seq_scan > 1000
		  AND seq_tup_read / GREATEST(seq_scan, 1) > 10000
		  AND schemaname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY seq_scan DESC
		LIMIT 20;
	`

	rows2, err := db.QueryContext(ctx, missingQuery)
	if err == nil {
		defer rows2.Close()

		for rows2.Next() {
			var schema, table string
			var seqScan, seqTupRead, idxScan int64

			if err := rows2.Scan(&schema, &table, &seqScan, &seqTupRead, &idxScan); err != nil {
				continue
			}

			suggestion := IndexSuggestion{
				Schema:               schema,
				Table:                table,
				SeqScans:             seqScan,
				Reason:               fmt.Sprintf("High sequential scan count (%d) with avg %d rows", seqScan, seqTupRead/max(seqScan, 1)),
				EstimatedImprovement: "Could reduce query time by 10-100x for filtered queries",
			}

			analysis.MissingIndexes = append(analysis.MissingIndexes, suggestion)
		}
	}

	return analysis, nil
}

// CollectTableStatistics collects table-level statistics
func CollectTableStatistics(ctx context.Context, db *sql.DB) ([]TableStatistics, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	query := `
		SELECT
			schemaname,
			tablename,
			seq_scan,
			seq_tup_read,
			idx_scan,
			idx_tup_fetch,
			n_tup_ins,
			n_tup_upd,
			n_tup_del,
			n_live_tup,
			n_dead_tup,
			vacuum_count,
			autovacuum_count,
			last_vacuum,
			last_autovacuum
		FROM pg_stat_user_tables
		WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY seq_scan DESC
		LIMIT 50;
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query table statistics: %w", err)
	}
	defer rows.Close()

	stats := []TableStatistics{}
	for rows.Next() {
		var s TableStatistics
		var lastVacuum, lastAutovacuum sql.NullTime

		if err := rows.Scan(
			&s.Schema,
			&s.Table,
			&s.SeqScans,
			&s.SeqTuplesRead,
			&s.IndexScans,
			&s.IndexTuplesRead,
			&s.InsertedTuples,
			&s.UpdatedTuples,
			&s.DeletedTuples,
			&s.LiveTuples,
			&s.DeadTuples,
			&s.VacuumCount,
			&s.AutovacuumCount,
			&lastVacuum,
			&lastAutovacuum,
		); err != nil {
			continue
		}

		if lastVacuum.Valid {
			s.LastVacuum = &lastVacuum.Time
		}
		if lastAutovacuum.Valid {
			s.LastAutovacuum = &lastAutovacuum.Time
		}

		stats = append(stats, s)
	}

	return stats, rows.Err()
}

// GenerateRecommendations creates optimization recommendations
func GenerateRecommendations(insights *PerformanceInsights, config *QueryPerformanceConfig) []PerformanceRecommendation {
	recommendations := []PerformanceRecommendation{}

	// Cache hit ratio recommendations
	if insights.CacheHitRatio < 0.90 {
		recommendations = append(recommendations, PerformanceRecommendation{
			Severity:        "high",
			Category:        "memory",
			Issue:           fmt.Sprintf("Low cache hit ratio: %.2f%%", insights.CacheHitRatio*100),
			Recommendation:  "Increase shared_buffers to improve cache hit ratio. Target: >95%",
			EstimatedImpact: "10-50% query performance improvement",
		})
	}

	// Slow query recommendations
	for _, query := range insights.SlowQueries {
		if query.MeanTime > 5*time.Second {
			recommendations = append(recommendations, PerformanceRecommendation{
				Severity:        "high",
				Category:        "query",
				QueryID:         query.QueryID,
				Query:           truncateQuery(query.Query, 100),
				Issue:           fmt.Sprintf("Very slow query: avg %.2fs", query.MeanTime.Seconds()),
				Recommendation:  "Review query plan with EXPLAIN ANALYZE. Consider adding indexes or rewriting query.",
				EstimatedImpact: "Could reduce execution time by 10-1000x",
			})
		}
	}

	// Unused index recommendations
	if len(insights.IndexAnalysis.UnusedIndexes) > 0 {
		totalSize := int64(0)
		for _, idx := range insights.IndexAnalysis.UnusedIndexes {
			totalSize += idx.Size
		}

		recommendations = append(recommendations, PerformanceRecommendation{
			Severity:        "medium",
			Category:        "indexes",
			Issue:           fmt.Sprintf("%d unused indexes consuming %d MB", len(insights.IndexAnalysis.UnusedIndexes), totalSize/(1024*1024)),
			Recommendation:  "Consider dropping unused indexes to save storage and reduce write overhead",
			EstimatedImpact: "5-10% improvement in INSERT/UPDATE performance",
		})
	}

	// Missing index recommendations
	if len(insights.IndexAnalysis.MissingIndexes) > 0 {
		recommendations = append(recommendations, PerformanceRecommendation{
			Severity:        "high",
			Category:        "indexes",
			Issue:           fmt.Sprintf("%d tables with high sequential scan counts", len(insights.IndexAnalysis.MissingIndexes)),
			Recommendation:  "Analyze queries on these tables and consider adding indexes",
			EstimatedImpact: "10-100x speedup for filtered queries",
		})
	}

	// Dead tuples recommendations
	for _, table := range insights.TableStatistics {
		if table.DeadTuples > table.LiveTuples/2 && table.DeadTuples > 1000 {
			recommendations = append(recommendations, PerformanceRecommendation{
				Severity:        "medium",
				Category:        "maintenance",
				Issue:           fmt.Sprintf("Table %s.%s has high dead tuple count: %d", table.Schema, table.Table, table.DeadTuples),
				Recommendation:  "Run VACUUM or adjust autovacuum settings for this table",
				EstimatedImpact: "Reduce table bloat and improve query performance",
			})
		}
	}

	return recommendations
}

// truncateQuery truncates query text to specified length
func truncateQuery(query string, maxLen int) string {
	query = strings.TrimSpace(query)
	if len(query) <= maxLen {
		return query
	}
	return query[:maxLen] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
