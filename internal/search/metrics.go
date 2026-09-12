package search

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// 计数描述处理尝试次数，并非去重后的业务任务数。提交中断后，
// 重放记录可能增加重复或过时结果的计数。
var indexAttempts = promauto.NewCounterVec(prometheus.CounterOpts{Name: "meshops_search_index_attempts_total", Help: "Task projection attempts by bounded outcome."}, []string{"outcome"})
var failedRecords = promauto.NewCounter(prometheus.CounterOpts{Name: "meshops_search_cdc_failed_attempts_total", Help: "Failed CDC handler attempts; Kafka offsets remain uncommitted."})
var appliedRecords = promauto.NewCounter(prometheus.CounterOpts{Name: "meshops_search_cdc_records_total", Help: "CDC records fully applied, including replayed records."})
var lastSuccess = promauto.NewGauge(prometheus.GaugeOpts{Name: "meshops_search_cdc_last_success_unixtime", Help: "Last successful CDC handler completion; idle sources also make this age grow."})
var queryDuration = promauto.NewHistogram(prometheus.HistogramOpts{Name: "meshops_search_query_duration_seconds", Help: "Search RPC processing duration, including errors.", Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}})
