//go:build integration

package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

func TestSuggestLokiOTelLabelConfigCollector(t *testing.T) {
	binary := os.Getenv("OTELCOL_CONTRIB")
	if binary == "" {
		t.Skip("Set OTELCOL_CONTRIB to an OpenTelemetry Collector contrib v0.162.0 binary")
	}
	versionContext, cancelVersion := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelVersion()
	version, err := exec.CommandContext(versionContext, binary, "--version").CombinedOutput()
	require.NoError(t, err, "%s", version)
	require.Contains(t, string(version), "0.162.0")

	for _, mode := range []string{"", "keep", "metadata", "drop"} {
		name := mode
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			args := SuggestLokiOTelLabelConfigParams{
				ApprovedLabels: []string{"service.name"}, OtherAttributes: mode,
			}
			escapedNames := []string{"${env:SECRET}", "quote\"\\\n", "control\x00\a\v\x1f and unicode \u2028"}
			if mode != "" {
				args.RequiredLabels = []string{"team"}
				args.MetadataAttributes = append([]string{"k8s.pod.name"}, escapedNames...)
				args.DropAttributes = []string{"secret"}
				args.NormalizeLogLevel = true
			}
			result, err := suggestLokiOTelLabelConfig(t.Context(), args)
			require.NoError(t, err)
			input := &collectorlogsv1.ExportLogsServiceRequest{}
			require.NoError(t, protojson.Unmarshal([]byte(`{
  "resourceLogs": [{
    "resource": {"attributes": [
      {"key": "service.name", "value": {"stringValue": "checkout"}},
      {"key": "k8s.pod.name", "value": {"stringValue": "pod-1"}},
      {"key": "secret", "value": {"stringValue": "resource-secret"}},
      {"key": "extra", "value": {"stringValue": "preserved"}},
      {"key": "count", "value": {"intValue": "7"}},
      {"key": "flag", "value": {"boolValue": false}},
      {"key": "nested", "value": {"kvlistValue": {"values": [
        {"key": "child", "value": {"arrayValue": {"values": [{"intValue": "1"}, {"stringValue": "two"}]}}}
      ]}}}
    ]},
    "scopeLogs": [{
      "scope": {"name": "first", "attributes": [{"key": "scope.key", "value": {"stringValue": "untouched"}}]},
      "logRecords": [{
        "body": {"stringValue": "first"},
        "attributes": [
          {"key": "k8s.pod.name", "value": {"stringValue": ""}},
          {"key": "secret", "value": {"stringValue": "log-secret"}},
          {"key": "level", "value": {"stringValue": "Info"}}
        ]
      }]
    }, {
      "scope": {"name": "second"},
      "logRecords": [{
        "body": {"stringValue": "second"},
        "attributes": [{"key": "k8s.pod.name", "value": {"intValue": "0"}}]
      }, {
        "body": {"stringValue": "third"},
        "attributes": [{"key": "level", "value": {"boolValue": false}}]
      }]
    }]
  }]
}`), input))
			for _, attributeName := range escapedNames {
				input.ResourceLogs[0].Resource.Attributes = append(input.ResourceLogs[0].Resource.Attributes, &commonv1.KeyValue{
					Key: attributeName, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "escaped"}},
				})
			}
			for _, value := range []*commonv1.AnyValue{
				{Value: &commonv1.AnyValue_StringValue{StringValue: ""}},
				{Value: &commonv1.AnyValue_IntValue{IntValue: 42}},
			} {
				resource := proto.Clone(input.ResourceLogs[0]).(*logsv1.ResourceLogs)
				resource.Resource.Attributes = append(resource.Resource.Attributes, &commonv1.KeyValue{Key: "team", Value: value})
				input.ResourceLogs = append(input.ResourceLogs, resource)
			}
			output := runOTelLabelCollector(t, binary, result.YAML, input)
			if mode == "" {
				assert.True(t, proto.Equal(input, output), "default configuration must preserve all data")
				return
			}
			require.Len(t, output.ResourceLogs, len(input.ResourceLogs))
			for resourceIndex, resource := range output.ResourceLogs {
				original := input.ResourceLogs[resourceIndex]
				attrs := otelCollectorAttributes(resource.Resource.Attributes)
				assert.Equal(t, "checkout", attrs["service.name"].GetStringValue())
				if resourceIndex == 2 {
					assert.EqualValues(t, 42, attrs["team"].GetIntValue())
				} else {
					assert.Equal(t, "unknown", attrs["team"].GetStringValue())
				}
				assert.NotContains(t, attrs, "k8s.pod.name")
				assert.NotContains(t, attrs, "secret")
				for _, attributeName := range escapedNames {
					assert.NotContains(t, attrs, attributeName)
				}
				require.Len(t, resource.ScopeLogs, len(original.ScopeLogs))
				for scopeIndex, scope := range resource.ScopeLogs {
					originalScope := original.ScopeLogs[scopeIndex]
					assert.True(t, proto.Equal(originalScope.Scope, scope.Scope))
					require.Len(t, scope.LogRecords, len(originalScope.LogRecords))
					for recordIndex, record := range scope.LogRecords {
						originalRecord := originalScope.LogRecords[recordIndex]
						assert.True(t, proto.Equal(originalRecord.Body, record.Body))
						logAttrs := otelCollectorAttributes(record.Attributes)
						expectedPod := otelCollectorAttributes(originalRecord.Attributes)["k8s.pod.name"]
						if expectedPod == nil {
							expectedPod = otelCollectorAttributes(original.Resource.Attributes)["k8s.pod.name"]
						}
						assert.True(t, proto.Equal(expectedPod, logAttrs["k8s.pod.name"]), "existing empty and numeric log values must win")
						for _, attributeName := range escapedNames {
							assert.Equal(t, "escaped", logAttrs[attributeName].GetStringValue())
						}
						for _, attributeName := range []string{"extra", "count", "flag", "nested"} {
							sourceValue := otelCollectorAttributes(original.Resource.Attributes)[attributeName]
							if mode == "keep" {
								assert.True(t, proto.Equal(sourceValue, attrs[attributeName]))
							} else {
								assert.NotContains(t, attrs, attributeName)
							}
							if mode == "metadata" {
								assert.True(t, proto.Equal(sourceValue, logAttrs[attributeName]))
							} else {
								assert.NotContains(t, logAttrs, attributeName)
							}
						}
						if scopeIndex == 0 {
							assert.Equal(t, "log-secret", logAttrs["secret"].GetStringValue())
							assert.Equal(t, "info", logAttrs["level"].GetStringValue())
						} else {
							assert.NotContains(t, logAttrs, "secret")
						}
						assert.NotContains(t, logAttrs, "service.name")
						assert.NotContains(t, logAttrs, "team")
					}
				}
			}
		})
	}
}

func otelCollectorAttributes(attributes []*commonv1.KeyValue) map[string]*commonv1.AnyValue {
	values := make(map[string]*commonv1.AnyValue, len(attributes))
	for _, attribute := range attributes {
		values[attribute.Key] = attribute.Value
	}
	return values
}

func runOTelLabelCollector(t *testing.T, binary, processorYAML string, input *collectorlogsv1.ExportLogsServiceRequest) *collectorlogsv1.ExportLogsServiceRequest {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	output := make(chan *collectorlogsv1.ExportLogsServiceRequest, 1)
	exporter := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		logs := &collectorlogsv1.ExportLogsServiceRequest{}
		if err := protojson.Unmarshal(payload, logs); err != nil {
			t.Errorf("decode Collector export: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case output <- logs:
		case <-ctx.Done():
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte("{}"))
	}))
	defer exporter.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	endpoint := listener.Addr().String()
	require.NoError(t, listener.Close())
	var config map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(processorYAML), &config))
	config["receivers"] = map[string]any{"otlp": map[string]any{"protocols": map[string]any{"http": map[string]any{"endpoint": endpoint}}}}
	config["exporters"] = map[string]any{"otlphttp": map[string]any{
		"endpoint": exporter.URL, "encoding": "json", "compression": "none",
		"retry_on_failure": map[string]any{"enabled": false},
	}}
	config["service"] = map[string]any{
		"telemetry": map[string]any{"metrics": map[string]any{"level": "none"}},
		"pipelines": map[string]any{"logs": map[string]any{
			"receivers": []string{"otlp"}, "processors": []string{"transform/enforce_labels"}, "exporters": []string{"otlphttp"},
		}},
	}
	rendered, err := yaml.Marshal(config)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "collector.yaml")
	require.NoError(t, os.WriteFile(path, rendered, 0600))
	validated, err := exec.CommandContext(ctx, binary, "validate", "--config", path).CombinedOutput()
	require.NoError(t, err, "Collector validation: %s", validated)
	logFile, err := os.CreateTemp(t.TempDir(), "collector-output")
	require.NoError(t, err)
	defer func() { _ = logFile.Close() }()
	command := exec.CommandContext(ctx, binary, "--config", path)
	command.Stdout, command.Stderr = logFile, logFile
	require.NoError(t, command.Start())
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	defer func() {
		cancel()
		<-done
		if t.Failed() {
			logs, _ := os.ReadFile(logFile.Name())
			t.Logf("Collector output: %s", logs)
		}
	}()
	payload, err := protojson.Marshal(input)
	require.NoError(t, err)
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/v1/logs", endpoint), bytes.NewReader(payload))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			require.NoError(t, readErr)
			require.Equal(t, http.StatusOK, response.StatusCode, "%s", body)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Collector did not accept logs: %v", err)
		case <-done:
			t.Fatal("Collector exited before accepting logs")
		case <-ticker.C:
		}
	}
	select {
	case logs := <-output:
		return logs
	case <-ctx.Done():
		t.Fatal("Collector did not export transformed logs")
	}
	return nil
}
