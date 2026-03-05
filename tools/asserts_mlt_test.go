//go:build unit
// +build unit

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPromLabels(t *testing.T) {
	tests := []struct {
		name       string
		entityType string
		entityName string
		env        string
		site       string
		namespace  string
		want       string
	}{
		{
			name:       "Service with namespace",
			entityType: "Service",
			entityName: "checkout",
			namespace:  "prod",
			want:       `{job="checkout", namespace="prod"}`,
		},
		{
			name:       "Service without namespace",
			entityType: "Service",
			entityName: "checkout",
			want:       `{job="checkout"}`,
		},
		{
			name:       "Node",
			entityType: "Node",
			entityName: "ip-10-0-1-5",
			want:       `{instance=~"ip-10-0-1-5.*"}`,
		},
		{
			name:       "Pod with namespace",
			entityType: "Pod",
			entityName: "checkout-abc123",
			namespace:  "prod",
			want:       `{pod="checkout-abc123", namespace="prod"}`,
		},
		{
			name:       "Namespace",
			entityType: "Namespace",
			entityName: "production",
			want:       `{namespace="production"}`,
		},
		{
			name:       "Unknown type falls back to job label",
			entityType: "Database",
			entityName: "postgres",
			namespace:  "prod",
			want:       `{job="postgres", namespace="prod"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPromLabels(tt.entityType, tt.entityName, tt.env, tt.site, tt.namespace)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildLokiLabels(t *testing.T) {
	tests := []struct {
		name       string
		entityType string
		entityName string
		namespace  string
		want       string
	}{
		{
			name:       "Service with namespace",
			entityType: "Service",
			entityName: "checkout",
			namespace:  "prod",
			want:       `{app="checkout", namespace="prod"}`,
		},
		{
			name:       "Pod with namespace",
			entityType: "Pod",
			entityName: "checkout-abc",
			namespace:  "prod",
			want:       `{pod="checkout-abc", namespace="prod"}`,
		},
		{
			name:       "Node",
			entityType: "Node",
			entityName: "ip-10-0-1-5",
			want:       `{node_name="ip-10-0-1-5"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildLokiLabels(tt.entityType, tt.entityName, "", "", tt.namespace)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildTraceAttrs(t *testing.T) {
	tests := []struct {
		name       string
		entityType string
		entityName string
		namespace  string
		want       string
	}{
		{
			name:       "Service with namespace",
			entityType: "Service",
			entityName: "checkout",
			namespace:  "prod",
			want:       `{resource.service.name="checkout" && resource.k8s.namespace.name="prod"}`,
		},
		{
			name:       "Pod with namespace",
			entityType: "Pod",
			entityName: "checkout-abc",
			namespace:  "prod",
			want:       `{resource.k8s.pod.name="checkout-abc" && resource.k8s.namespace.name="prod"}`,
		},
		{
			name:       "Unknown type falls back to service.name",
			entityType: "Database",
			entityName: "postgres",
			want:       `{resource.service.name="postgres"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTraceAttrs(tt.entityType, tt.entityName, "", "", tt.namespace)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestInjectLabels(t *testing.T) {
	tests := []struct {
		name     string
		template string
		labels   string
		want     string
	}{
		{
			name:     "simple metric with labels",
			template: `up%s`,
			labels:   `{job="checkout"}`,
			want:     `up{job="checkout"}`,
		},
		{
			name:     "metric with existing braces",
			template: `rate(http_server_requests_seconds_count{status=~"5.."%s}[5m])`,
			labels:   `{job="checkout", namespace="prod"}`,
			want:     `rate(http_server_requests_seconds_count{status=~"5..", job="checkout", namespace="prod"}[5m])`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectLabels(tt.template, tt.labels)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsBareMetricName(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{
			name:  "simple metric name",
			query: "up",
			want:  true,
		},
		{
			name:  "metric name with underscores",
			query: "http_requests_total",
			want:  true,
		},
		{
			name:  "metric name with colon",
			query: "node:cpu:ratio",
			want:  true,
		},
		{
			name:  "rate function",
			query: "rate(http_requests_total[5m])",
			want:  false,
		},
		{
			name:  "sum aggregation",
			query: "sum(container_cpu_usage_seconds_total) by (pod)",
			want:  false,
		},
		{
			name:  "metric with label selector",
			query: "http_requests_total{job=\"api\"}",
			want:  false,
		},
		{
			name:  "binary operation",
			query: "metric_a / metric_b",
			want:  false,
		},
		{
			name:  "comparison",
			query: "metric > 100",
			want:  false,
		},
		{
			name:  "histogram quantile",
			query: "histogram_quantile(0.99, rate(metric_bucket[5m]))",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBareMetricName(tt.query)
			assert.Equal(t, tt.want, got)
		})
	}
}
