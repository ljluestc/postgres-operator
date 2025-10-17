// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbouncer

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestPoolAnalyticsConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := &PoolAnalyticsConfig{
			Enabled:                  true,
			CollectionInterval:       30 * time.Second,
			UtilizationThreshold:     0.80,
			WaitTimeThreshold:        5 * time.Second,
			RecommendationEnabled:    true,
		}

		assert.Assert(t, config.Enabled)
		assert.Equal(t, config.UtilizationThreshold, 0.80)
		assert.Assert(t, config.RecommendationEnabled)
	})

	t.Run("CustomThresholds", func(t *testing.T) {
		config := &PoolAnalyticsConfig{
			UtilizationThreshold:  0.90,
			WaitTimeThreshold:     10 * time.Second,
			RecommendationEnabled: false,
		}

		assert.Equal(t, config.UtilizationThreshold, 0.90)
		assert.Assert(t, !config.RecommendationEnabled)
	})
}

func TestPoolStatistics(t *testing.T) {
	t.Run("BasicStats", func(t *testing.T) {
		stats := PoolStatistics{
			Database:          "postgres",
			User:              "pguser",
			Active:            10,
			Waiting:           2,
			MaxConnections:    25,
			Utilization:       0.40,
			QueriesPerSecond:  100.5,
			AverageQueryTime:  50 * time.Millisecond,
			MaxWaitTime:       2 * time.Second,
			TotalQueries:      10000,
			TotalTransactions: 9500,
			BytesReceived:     1024 * 1024 * 100,
			BytesSent:         1024 * 1024 * 50,
		}

		assert.Equal(t, stats.Database, "postgres")
		assert.Equal(t, stats.Active, 10)
		assert.Equal(t, stats.Waiting, 2)
		assert.Assert(t, stats.Utilization < 1.0)
	})

	t.Run("HighUtilization", func(t *testing.T) {
		stats := PoolStatistics{
			Active:         20,
			MaxConnections: 25,
			Utilization:    0.80,
		}

		assert.Assert(t, stats.Utilization >= 0.80)
	})
}

func TestPoolAnalytics(t *testing.T) {
	t.Run("WithMultiplePools", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Timestamp: time.Now(),
			Pools: []PoolStatistics{
				{Database: "db1", Active: 10, MaxConnections: 25},
				{Database: "db2", Active: 5, MaxConnections: 25},
			},
			OverallUtilization:  0.30,
			TotalWaitingClients: 0,
		}

		assert.Equal(t, len(analytics.Pools), 2)
		assert.Equal(t, analytics.OverallUtilization, 0.30)
	})

	t.Run("WithHighUtilization", func(t *testing.T) {
		analytics := &PoolAnalytics{
			HighUtilizationPools: []string{"db1", "db2"},
			PoolsWithWaits:       []string{"db1"},
		}

		assert.Equal(t, len(analytics.HighUtilizationPools), 2)
		assert.Equal(t, len(analytics.PoolsWithWaits), 1)
	})

	t.Run("WithRecommendations", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Recommendations: []PoolRecommendation{
				{
					Severity:       "high",
					Pool:           "db1",
					Issue:          "High utilization",
					Recommendation: "Increase pool size",
				},
			},
		}

		assert.Equal(t, len(analytics.Recommendations), 1)
		assert.Equal(t, analytics.Recommendations[0].Severity, "high")
	})
}

func TestPoolRecommendation(t *testing.T) {
	t.Run("HighSeverityRecommendation", func(t *testing.T) {
		rec := PoolRecommendation{
			Severity:        "high",
			Pool:            "postgres",
			Issue:           "Pool at 95% utilization",
			Recommendation:  "Increase pool size from 25 to 40",
			CurrentValue:    "25 connections",
			SuggestedValue:  "40 connections",
			EstimatedImpact: "Eliminate connection queuing",
		}

		assert.Equal(t, rec.Severity, "high")
		assert.Equal(t, rec.Pool, "postgres")
	})

	t.Run("LowSeverityRecommendation", func(t *testing.T) {
		rec := PoolRecommendation{
			Severity:       "low",
			Pool:           "test_db",
			Issue:          "Pool underutilized at 10%",
			Recommendation: "Reduce pool size to save resources",
		}

		assert.Equal(t, rec.Severity, "low")
	})
}

func TestGeneratePoolRecommendations(t *testing.T) {
	t.Run("HighOverallUtilization", func(t *testing.T) {
		analytics := &PoolAnalytics{
			OverallUtilization: 0.85,
			Pools: []PoolStatistics{
				{Database: "db1", Utilization: 0.85},
			},
		}
		config := &PoolAnalyticsConfig{
			UtilizationThreshold: 0.80,
		}

		recommendations := GeneratePoolRecommendations(analytics, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("HighPoolUtilization", func(t *testing.T) {
		analytics := &PoolAnalytics{
			OverallUtilization: 0.50,
			Pools: []PoolStatistics{
				{
					Database:       "db1",
					Utilization:    0.95,
					MaxConnections: 25,
				},
			},
		}
		config := &PoolAnalyticsConfig{}

		recommendations := GeneratePoolRecommendations(analytics, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("WaitingClients", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Pools: []PoolStatistics{
				{
					Database: "db1",
					Active:   20,
					Waiting:  10,
				},
			},
		}
		config := &PoolAnalyticsConfig{}

		recommendations := GeneratePoolRecommendations(analytics, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("LongWaitTimes", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Pools: []PoolStatistics{
				{
					Database:    "db1",
					MaxWaitTime: 10 * time.Second,
				},
			},
		}
		config := &PoolAnalyticsConfig{}

		recommendations := GeneratePoolRecommendations(analytics, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("LowUtilization", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Pools: []PoolStatistics{
				{
					Database:       "db1",
					Utilization:    0.15,
					MaxConnections: 50,
				},
			},
		}
		config := &PoolAnalyticsConfig{}

		recommendations := GeneratePoolRecommendations(analytics, config)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("HealthySystem", func(t *testing.T) {
		analytics := &PoolAnalytics{
			OverallUtilization:  0.40,
			TotalWaitingClients: 0,
		}
		config := &PoolAnalyticsConfig{}

		recommendations := GeneratePoolRecommendations(analytics, config)
		assert.Assert(t, len(recommendations) > 0)
	})
}

func TestCalculateOptimalPoolSize(t *testing.T) {
	t.Run("LowLoad", func(t *testing.T) {
		size := CalculateOptimalPoolSize(5.0, 100*time.Millisecond, 1*time.Second)
		assert.Assert(t, size >= 10) // Minimum is 10
	})

	t.Run("HighLoad", func(t *testing.T) {
		size := CalculateOptimalPoolSize(1000.0, 500*time.Millisecond, 1*time.Second)
		assert.Assert(t, size <= 200) // Maximum is 200
	})

	t.Run("ModerateLoad", func(t *testing.T) {
		size := CalculateOptimalPoolSize(50.0, 200*time.Millisecond, 1*time.Second)
		assert.Assert(t, size >= 10)
		assert.Assert(t, size <= 200)
	})

	t.Run("VeryLowLoad", func(t *testing.T) {
		size := CalculateOptimalPoolSize(1.0, 10*time.Millisecond, 1*time.Second)
		assert.Equal(t, size, 10) // Should hit minimum
	})

	t.Run("VeryHighLoad", func(t *testing.T) {
		size := CalculateOptimalPoolSize(10000.0, 2*time.Second, 1*time.Second)
		assert.Equal(t, size, 200) // Should hit maximum
	})
}

func TestGeneratePoolReport(t *testing.T) {
	t.Run("BasicReport", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Timestamp:          time.Now(),
			OverallUtilization: 0.60,
			TotalWaitingClients: 5,
			Pools: []PoolStatistics{
				{
					Database:         "postgres",
					User:             "pguser",
					Active:           15,
					Waiting:          5,
					Utilization:      0.60,
					MaxWaitTime:      2 * time.Second,
					TotalQueries:     10000,
					TotalTransactions: 9500,
					AverageQueryTime: 50 * time.Millisecond,
				},
			},
			Recommendations: []PoolRecommendation{
				{
					Severity:       "medium",
					Pool:           "postgres",
					Issue:          "Some waiting clients",
					Recommendation: "Consider increasing pool size",
				},
			},
		}

		report := GeneratePoolReport(analytics)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("WithHighUtilization", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Timestamp:            time.Now(),
			OverallUtilization:   0.90,
			HighUtilizationPools: []string{"db1", "db2"},
			PoolsWithWaits:       []string{"db1"},
			Pools: []PoolStatistics{
				{Database: "db1", Utilization: 0.95},
			},
		}

		report := GeneratePoolReport(analytics)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("NoRecommendations", func(t *testing.T) {
		analytics := &PoolAnalytics{
			Timestamp:          time.Now(),
			OverallUtilization: 0.50,
			Pools: []PoolStatistics{
				{Database: "postgres"},
			},
			Recommendations: []PoolRecommendation{},
		}

		report := GeneratePoolReport(analytics)
		assert.Assert(t, len(report) > 0)
	})
}

func TestExportMetrics(t *testing.T) {
	t.Run("ExportMultiplePools", func(t *testing.T) {
		pools := []PoolStatistics{
			{
				Database:          "db1",
				User:              "user1",
				Active:            10,
				Waiting:           2,
				Utilization:       0.40,
				MaxWaitTime:       1 * time.Second,
				TotalQueries:      1000,
				TotalTransactions: 950,
				BytesReceived:     1024 * 1024,
				BytesSent:         512 * 1024,
			},
			{
				Database:          "db2",
				User:              "user2",
				Active:            5,
				Waiting:           0,
				Utilization:       0.20,
				MaxWaitTime:       0,
				TotalQueries:      500,
				TotalTransactions: 475,
				BytesReceived:     512 * 1024,
				BytesSent:         256 * 1024,
			},
		}

		// This should not panic
		ExportMetrics("test-cluster", "test-ns", pools)
	})
}

func TestCollectPoolStatisticsErrors(t *testing.T) {
	t.Run("NilDatabase", func(t *testing.T) {
		ctx := context.Background()

		_, err := CollectPoolStatistics(ctx, nil)
		assert.Assert(t, err != nil)
	})
}

func TestAnalyzePoolPerformanceErrors(t *testing.T) {
	t.Run("NilDatabase", func(t *testing.T) {
		ctx := context.Background()
		config := &PoolAnalyticsConfig{
			UtilizationThreshold:  0.80,
			RecommendationEnabled: true,
		}

		_, err := AnalyzePoolPerformance(ctx, nil, config)
		assert.Assert(t, err != nil)
	})
}

func TestTrendAnalysis(t *testing.T) {
	t.Run("EmptyTrend", func(t *testing.T) {
		trend := &TrendAnalysis{
			UtilizationTrend: []TrendPoint{},
			WaitTimeTrend:    []TrendPoint{},
			ThroughputTrend:  []TrendPoint{},
		}

		assert.Equal(t, len(trend.UtilizationTrend), 0)
	})

	t.Run("WithData", func(t *testing.T) {
		trend := &TrendAnalysis{
			UtilizationTrend: []TrendPoint{
				{Timestamp: time.Now(), Value: 0.50},
				{Timestamp: time.Now(), Value: 0.60},
				{Timestamp: time.Now(), Value: 0.70},
			},
		}

		assert.Equal(t, len(trend.UtilizationTrend), 3)
	})
}

func TestTrendPoint(t *testing.T) {
	t.Run("BasicPoint", func(t *testing.T) {
		point := TrendPoint{
			Timestamp: time.Now(),
			Value:     0.75,
		}

		assert.Assert(t, point.Value > 0)
	})
}
