// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// mockDB implements a minimal sql.DB interface for testing
type mockResult struct{}

func (m mockResult) LastInsertId() (int64, error) { return 0, nil }
func (m mockResult) RowsAffected() (int64, error) { return 0, nil }

type mockRows struct {
	columns []string
	values  [][]driver.Value
	pos     int
}

func (m *mockRows) Columns() []string {
	return m.columns
}

func (m *mockRows) Close() error {
	return nil
}

func (m *mockRows) Next(dest []driver.Value) error {
	if m.pos >= len(m.values) {
		return driver.ErrSkip
	}
	copy(dest, m.values[m.pos])
	m.pos++
	return nil
}

func TestQueryPerformanceConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := &QueryPerformanceConfig{
			Enabled:            true,
			SlowQueryThreshold: 1 * time.Second,
			TopQueriesCount:    10,
			CollectionInterval: 1 * time.Minute,
		}

		assert.Assert(t, config.Enabled)
		assert.Equal(t, config.SlowQueryThreshold, 1*time.Second)
		assert.Equal(t, config.TopQueriesCount, 10)
	})

	t.Run("CustomThresholds", func(t *testing.T) {
		config := &QueryPerformanceConfig{
			SlowQueryThreshold: 5 * time.Second,
			TopQueriesCount:    20,
			NormalizeQueries:   true,
			TrackIOTiming:      true,
		}

		assert.Assert(t, config.NormalizeQueries)
		assert.Assert(t, config.TrackIOTiming)
	})
}

func TestQueryStatistics(t *testing.T) {
	t.Run("StatisticsStructure", func(t *testing.T) {
		stats := QueryStatistics{
			QueryID:   "12345",
			Query:     "SELECT * FROM users",
			Database:  "postgres",
			User:      "pguser",
			Calls:     100,
			TotalTime: 10 * time.Second,
			MinTime:   50 * time.Millisecond,
			MaxTime:   500 * time.Millisecond,
			MeanTime:  100 * time.Millisecond,
			Rows:      1000,
		}

		assert.Equal(t, stats.QueryID, "12345")
		assert.Equal(t, stats.Calls, int64(100))
		assert.Equal(t, stats.TotalTime, 10*time.Second)
	})
}

func TestPerformanceInsights(t *testing.T) {
	t.Run("InsightsWithSlowQueries", func(t *testing.T) {
		insights := &PerformanceInsights{
			Timestamp: time.Now(),
			SlowQueries: []QueryStatistics{
				{
					QueryID:  "1",
					Query:    "SELECT * FROM large_table",
					MeanTime: 10 * time.Second,
				},
			},
			CacheHitRatio: 0.95,
		}

		assert.Assert(t, len(insights.SlowQueries) > 0)
		assert.Assert(t, insights.CacheHitRatio > 0.90)
	})

	t.Run("InsightsWithRecommendations", func(t *testing.T) {
		insights := &PerformanceInsights{
			Recommendations: []PerformanceRecommendation{
				{
					Severity:        "high",
					Category:        "query",
					Issue:           "Slow query detected",
					Recommendation:  "Add index",
					EstimatedImpact: "10x speedup",
				},
			},
		}

		assert.Equal(t, len(insights.Recommendations), 1)
		assert.Equal(t, insights.Recommendations[0].Severity, "high")
	})
}

func TestPerformanceRecommendation(t *testing.T) {
	t.Run("HighSeverityRecommendation", func(t *testing.T) {
		rec := PerformanceRecommendation{
			Severity:        "high",
			Category:        "memory",
			QueryID:         "123",
			Query:           "SELECT COUNT(*) FROM users",
			Issue:           "High memory usage",
			Recommendation:  "Increase shared_buffers",
			EstimatedImpact: "20% improvement",
		}

		assert.Equal(t, rec.Severity, "high")
		assert.Equal(t, rec.Category, "memory")
	})

	t.Run("IndexRecommendation", func(t *testing.T) {
		rec := PerformanceRecommendation{
			Severity:        "medium",
			Category:        "indexes",
			Issue:           "Missing index",
			Recommendation:  "CREATE INDEX ON users(email)",
			EstimatedImpact: "100x speedup",
		}

		assert.Equal(t, rec.Category, "indexes")
	})
}

func TestIndexAnalysis(t *testing.T) {
	t.Run("UnusedIndexes", func(t *testing.T) {
		analysis := IndexAnalysis{
			UnusedIndexes: []IndexInfo{
				{
					Schema: "public",
					Table:  "users",
					Index:  "idx_unused",
					Size:   1024 * 1024,
					Scans:  0,
				},
			},
		}

		assert.Equal(t, len(analysis.UnusedIndexes), 1)
		assert.Equal(t, analysis.UnusedIndexes[0].Scans, int64(0))
	})

	t.Run("MissingIndexes", func(t *testing.T) {
		analysis := IndexAnalysis{
			MissingIndexes: []IndexSuggestion{
				{
					Schema:               "public",
					Table:                "orders",
					Columns:              []string{"user_id", "created_at"},
					Reason:               "High sequential scan count",
					SeqScans:             10000,
					EstimatedImprovement: "50x speedup",
				},
			},
		}

		assert.Equal(t, len(analysis.MissingIndexes), 1)
		assert.Equal(t, analysis.MissingIndexes[0].SeqScans, int64(10000))
	})
}

func TestIndexInfo(t *testing.T) {
	t.Run("IndexWithScans", func(t *testing.T) {
		info := IndexInfo{
			Schema:        "public",
			Table:         "users",
			Index:         "idx_users_email",
			Size:          1024 * 1024 * 10,
			Scans:         10000,
			TuplesRead:    50000,
			TuplesFetched: 45000,
		}

		assert.Equal(t, info.Schema, "public")
		assert.Assert(t, info.Scans > 0)
	})
}

func TestTableStatistics(t *testing.T) {
	t.Run("TableWithDeadTuples", func(t *testing.T) {
		stats := TableStatistics{
			Schema:         "public",
			Table:          "users",
			SeqScans:       100,
			IndexScans:     10000,
			LiveTuples:     1000,
			DeadTuples:     500,
			VacuumCount:    5,
			AutovacuumCount: 10,
		}

		assert.Equal(t, stats.Table, "users")
		assert.Assert(t, stats.DeadTuples > 0)
		assert.Assert(t, stats.IndexScans > stats.SeqScans)
	})

	t.Run("TableWithVacuumTimes", func(t *testing.T) {
		now := time.Now()
		stats := TableStatistics{
			Schema:         "public",
			Table:          "orders",
			LastVacuum:     &now,
			LastAutovacuum: &now,
		}

		assert.Assert(t, stats.LastVacuum != nil)
		assert.Assert(t, stats.LastAutovacuum != nil)
	})
}

func TestGenerateRecommendations(t *testing.T) {
	t.Run("LowCacheHitRatio", func(t *testing.T) {
		insights := &PerformanceInsights{
			CacheHitRatio: 0.85,
		}
		config := &QueryPerformanceConfig{
			SlowQueryThreshold: 1 * time.Second,
		}

		recommendations := GenerateRecommendations(insights, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("SlowQueries", func(t *testing.T) {
		insights := &PerformanceInsights{
			SlowQueries: []QueryStatistics{
				{
					QueryID:  "1",
					Query:    "SELECT * FROM huge_table WHERE unindexed_column = 'value'",
					MeanTime: 10 * time.Second,
				},
			},
			CacheHitRatio: 0.95,
		}
		config := &QueryPerformanceConfig{
			SlowQueryThreshold: 1 * time.Second,
		}

		recommendations := GenerateRecommendations(insights, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("UnusedIndexes", func(t *testing.T) {
		insights := &PerformanceInsights{
			CacheHitRatio: 0.95,
			IndexAnalysis: IndexAnalysis{
				UnusedIndexes: []IndexInfo{
					{Size: 1024 * 1024 * 100},
					{Size: 1024 * 1024 * 50},
				},
			},
		}
		config := &QueryPerformanceConfig{
			SlowQueryThreshold: 1 * time.Second,
		}

		recommendations := GenerateRecommendations(insights, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("MissingIndexes", func(t *testing.T) {
		insights := &PerformanceInsights{
			CacheHitRatio: 0.95,
			IndexAnalysis: IndexAnalysis{
				MissingIndexes: []IndexSuggestion{
					{Table: "table1"},
					{Table: "table2"},
				},
			},
		}
		config := &QueryPerformanceConfig{
			SlowQueryThreshold: 1 * time.Second,
		}

		recommendations := GenerateRecommendations(insights, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("DeadTuples", func(t *testing.T) {
		insights := &PerformanceInsights{
			CacheHitRatio: 0.95,
			TableStatistics: []TableStatistics{
				{
					Schema:     "public",
					Table:      "users",
					LiveTuples: 1000,
					DeadTuples: 2000,
				},
			},
		}
		config := &QueryPerformanceConfig{
			SlowQueryThreshold: 1 * time.Second,
		}

		recommendations := GenerateRecommendations(insights, config)
		assert.Assert(t, len(recommendations) > 0)
	})
}

func TestTruncateQuery(t *testing.T) {
	t.Run("ShortQuery", func(t *testing.T) {
		query := "SELECT * FROM users"
		result := truncateQuery(query, 100)
		assert.Equal(t, result, query)
	})

	t.Run("LongQuery", func(t *testing.T) {
		query := "SELECT * FROM users WHERE id IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20)"
		result := truncateQuery(query, 50)
		assert.Assert(t, len(result) <= 53) // 50 + "..."
		assert.Assert(t, result[len(result)-3:] == "...")
	})

	t.Run("QueryWithWhitespace", func(t *testing.T) {
		query := "   SELECT * FROM users   "
		result := truncateQuery(query, 100)
		assert.Equal(t, result, "SELECT * FROM users")
	})
}

func TestMinMax(t *testing.T) {
	t.Run("MinFunction", func(t *testing.T) {
		assert.Equal(t, min(5, 10), 5)
		assert.Equal(t, min(10, 5), 5)
		assert.Equal(t, min(5, 5), 5)
	})

	t.Run("MaxFunction", func(t *testing.T) {
		assert.Equal(t, max(int64(5), int64(10)), int64(10))
		assert.Equal(t, max(int64(10), int64(5)), int64(10))
		assert.Equal(t, max(int64(5), int64(5)), int64(5))
	})
}

func TestIndexSuggestion(t *testing.T) {
	t.Run("BasicSuggestion", func(t *testing.T) {
		suggestion := IndexSuggestion{
			Schema:               "public",
			Table:                "orders",
			Columns:              []string{"user_id", "status"},
			Reason:               "High sequential scan count",
			SeqScans:             5000,
			EstimatedImprovement: "10-100x speedup",
		}

		assert.Equal(t, suggestion.Schema, "public")
		assert.Equal(t, len(suggestion.Columns), 2)
		assert.Assert(t, suggestion.SeqScans > 0)
	})
}

func TestCollectQueryStatisticsErrors(t *testing.T) {
	t.Run("NilDatabase", func(t *testing.T) {
		ctx := context.Background()
		config := &QueryPerformanceConfig{
			TopQueriesCount: 10,
		}

		_, err := CollectQueryStatistics(ctx, nil, config)
		assert.Assert(t, err != nil)
	})
}

func TestAnalyzePerformanceErrors(t *testing.T) {
	t.Run("NilDatabase", func(t *testing.T) {
		ctx := context.Background()
		config := &QueryPerformanceConfig{
			SlowQueryThreshold: 1 * time.Second,
			TopQueriesCount:    10,
		}

		_, err := AnalyzePerformance(ctx, nil, config)
		assert.Assert(t, err != nil)
	})
}

func TestCalculateCacheHitRatioErrors(t *testing.T) {
	t.Run("NilDatabase", func(t *testing.T) {
		ctx := context.Background()

		_, err := CalculateCacheHitRatio(ctx, nil)
		assert.Assert(t, err != nil)
	})
}

func TestAnalyzeIndexesErrors(t *testing.T) {
	t.Run("NilDatabase", func(t *testing.T) {
		ctx := context.Background()

		_, err := AnalyzeIndexes(ctx, nil)
		assert.Assert(t, err != nil)
	})
}

func TestCollectTableStatisticsErrors(t *testing.T) {
	t.Run("NilDatabase", func(t *testing.T) {
		ctx := context.Background()

		_, err := CollectTableStatistics(ctx, nil)
		assert.Assert(t, err != nil)
	})
}
