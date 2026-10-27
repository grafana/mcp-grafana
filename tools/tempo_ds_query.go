package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Grafana's Tempo datasource answers trace-by-ID queries through
// /api/ds/query in every release this server supports, TraceQL metrics from
// 11.5 and TraceQL search from 12.3. Older releases keep using the proxy.
var (
	tempoDSQueryMetricsSince = [2]int{11, 5}
	tempoDSQuerySearchSince  = [2]int{12, 3}
)

// useDSQuery reports whether queries should go through /api/ds/query rather
// than the datasource proxy. since is the first Grafana release whose Tempo
// datasource supports the query type; an unknown version counts as new.
func (b *tempoBackend) useDSQuery(ctx context.Context, since [2]int) bool {
	if b.legacy {
		return false
	}
	major, minor, ok := parseMajorMinor(mcpgrafana.GrafanaVersion(ctx))
	return !ok || major > since[0] || (major == since[0] && minor >= since[1])
}

// tempoTimeRange resolves optional start and end times, defaulting to the
// past hour like the proxy path does.
func tempoTimeRange(start, end string) (time.Time, time.Time, error) {
	to := time.Now()
	from := to.Add(-time.Hour)
	var err error
	if start != "" {
		if from, err = parseStartTime(start); err != nil {
			return from, to, fmt.Errorf("invalid start time: %v", err)
		}
	}
	if end != "" {
		if to, err = parseEndTime(end); err != nil {
			return from, to, fmt.Errorf("invalid end time: %v", err)
		}
	}
	return from, to, nil
}

// dsQuery runs one Tempo query through /api/ds/query and returns its frames.
func (b *tempoBackend) dsQuery(ctx context.Context, from, to time.Time, query map[string]any) (data.Frames, error) {
	query["refId"] = "A"
	query["datasource"] = map[string]string{"uid": b.dsUID, "type": "tempo"}
	resp, err := doDSQuery(ctx, b.dsQueryClient, b.grafanaURL, dsQueryPayload(from, to, query))
	if err != nil {
		return nil, fmt.Errorf("querying Tempo: %w", err)
	}
	r, ok := resp.Responses["A"]
	if !ok {
		return nil, nil
	}
	if r.Error != nil {
		return nil, fmt.Errorf("querying Tempo: %s", r.Error.Error())
	}
	return r.Frames, nil
}

// tempoRawSearchResponse returns the Tempo search response that Grafana's
// "raw" table type wraps in a single string field.
func tempoRawSearchResponse(frames data.Frames) (string, error) {
	for _, frame := range frames {
		if f, _ := frame.FieldByName("response"); f != nil && f.Len() > 0 {
			if v, ok := f.ConcreteAt(0); ok {
				if s, ok := v.(string); ok {
					return s, nil
				}
			}
		}
	}
	return "", fmt.Errorf("tempo datasource returned no raw search response")
}

// flattenTempoAttributes rewrites every "attributes" list of {key, value}
// pairs below v into a {key: value} object. Values arrive as OTLP JSON
// ({"stringValue": "GET"}) from Tempo, or wrapped in a Go oneof
// ({"Value": {"string_value": "GET"}}) from Grafana's raw search frame.
func flattenTempoAttributes(v any) {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if list, ok := child.([]any); ok && k == "attributes" {
				attrs := make(map[string]any, len(list))
				for _, item := range list {
					kv, _ := item.(map[string]any)
					key, _ := kv["key"].(string)
					attrs[key] = tempoAttributeValue(kv["value"])
				}
				v[k] = attrs
				continue
			}
			flattenTempoAttributes(child)
		}
	case []any:
		for _, child := range v {
			flattenTempoAttributes(child)
		}
	}
}

func tempoAttributeValue(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if inner, ok := m["Value"].(map[string]any); ok {
		m = inner
	}
	for k, value := range m {
		// OTLP JSON encodes 64-bit integers as strings.
		if s, ok := value.(string); ok && (k == "intValue" || k == "int_value") {
			return json.Number(s)
		}
		return value
	}
	return nil
}

// tempoMetricSeries is one series of a TraceQL metrics result.
type tempoMetricSeries struct {
	Labels    map[string]string `json:"labels"`
	Value     *float64          `json:"value,omitempty"`
	Samples   []tempoSample     `json:"samples,omitempty"`
	Exemplars []tempoExemplar   `json:"exemplars,omitempty"`
}

type tempoSample struct {
	TimestampMs int64   `json:"timestampMs"`
	Value       float64 `json:"value"`
}

type tempoExemplar struct {
	TraceID     string  `json:"traceId"`
	TimestampMs int64   `json:"timestampMs"`
	Value       float64 `json:"value"`
}

// tempoMetricsFromFrames converts the frames of a TraceQL metrics query into
// series. Range queries return a time/value frame per series plus an
// "exemplar" frame per series that has exemplars; instant queries return one
// row per series, stamped with the time Grafana ran the query.
func tempoMetricsFromFrames(frames data.Frames, instant bool) []tempoMetricSeries {
	series := []tempoMetricSeries{}
	byName := map[string]int{}
	var exemplarFrames []*data.Frame
	for _, frame := range frames {
		if frame.Meta != nil && frame.Meta.DataTopic == data.DataTopicAnnotations {
			exemplarFrames = append(exemplarFrames, frame)
			continue
		}
		timeIdx, valueIdx := findTimeAndValueFields(frame.Fields)
		if valueIdx == -1 {
			continue
		}
		s := tempoMetricSeries{Labels: map[string]string{}}
		for k, v := range frame.Fields[valueIdx].Labels {
			// Grafana quotes string label values to tell "1" from 1.
			if unquoted, err := strconv.Unquote(v); err == nil {
				v = unquoted
			}
			s.Labels[k] = v
		}
		for i := 0; i < frame.Rows(); i++ {
			val, ok := toFloat(frame.At(valueIdx, i))
			if !ok {
				continue
			}
			if instant {
				s.Value = &val
				continue
			}
			if timeIdx == -1 {
				continue
			}
			if ts, ok := toMillis(frame.At(timeIdx, i)); ok {
				s.Samples = append(s.Samples, tempoSample{TimestampMs: ts, Value: val})
			}
		}
		byName[frame.Name] = len(series)
		series = append(series, s)
	}
	for _, frame := range exemplarFrames {
		i, ok := byName[frame.RefID]
		if !ok {
			continue
		}
		timeField, _ := frame.FieldByName("Time")
		valueField, _ := frame.FieldByName("Value")
		traceField, _ := frame.FieldByName("traceId")
		if timeField == nil || valueField == nil || traceField == nil {
			continue
		}
		for row := 0; row < frame.Rows(); row++ {
			ts, tsOK := toMillis(timeField.At(row))
			val, valOK := toFloat(valueField.At(row))
			traceID, _ := traceField.ConcreteAt(row)
			id, _ := traceID.(string)
			if tsOK && valOK {
				series[i].Exemplars = append(series[i].Exemplars, tempoExemplar{TraceID: id, TimestampMs: ts, Value: val})
			}
		}
	}
	return series
}

// tempoTrace is the get_tempo_trace output: the spans of Grafana's trace
// frame, grouped by the resource they were emitted from.
type tempoTrace struct {
	TraceID  string              `json:"traceId"`
	Partial  bool                `json:"partial,omitempty"`
	Message  string              `json:"message,omitempty"`
	Services []tempoTraceService `json:"services"`
}

type tempoTraceService struct {
	ServiceName string           `json:"serviceName"`
	Resource    map[string]any   `json:"resource,omitempty"`
	Spans       []tempoTraceSpan `json:"spans"`
}

type tempoTraceSpan struct {
	SpanID        string           `json:"spanId"`
	ParentSpanID  string           `json:"parentSpanId,omitempty"`
	Name          string           `json:"name"`
	Kind          string           `json:"kind,omitempty"`
	Scope         string           `json:"scope,omitempty"`
	ScopeVersion  string           `json:"scopeVersion,omitempty"`
	StartTimeMs   float64          `json:"startTimeMs"`
	DurationMs    float64          `json:"durationMs"`
	Status        string           `json:"status,omitempty"`
	StatusMessage string           `json:"statusMessage,omitempty"`
	TraceState    string           `json:"traceState,omitempty"`
	Attributes    map[string]any   `json:"attributes,omitempty"`
	Events        []TraceEvent     `json:"events,omitempty"`
	Links         []tempoTraceLink `json:"links,omitempty"`
}

type tempoTraceLink struct {
	TraceID    string         `json:"traceId"`
	SpanID     string         `json:"spanId"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// tempoTraceFromFrames converts Grafana's trace frame (one row per span) into
// a tempoTrace, and into the spans the trace viewer displays. displaySpans is
// nil when the trace is partial or too large to display.
func tempoTraceFromFrames(traceID string, frames data.Frames) (*tempoTrace, []TraceSpan, error) {
	if len(frames) == 0 || frames[0].Rows() == 0 {
		return nil, nil, fmt.Errorf("trace %s not found", traceID)
	}
	frame := frames[0]
	str := func(name string, row int) string {
		f, _ := frame.FieldByName(name)
		if f == nil {
			return ""
		}
		v, _ := f.ConcreteAt(row)
		s, _ := v.(string)
		return s
	}
	num := func(name string, row int) float64 {
		f, _ := frame.FieldByName(name)
		if f == nil {
			return 0
		}
		v, _ := toFloat(f.At(row))
		return v
	}
	raw := func(name string, row int) json.RawMessage {
		f, _ := frame.FieldByName(name)
		if f == nil {
			return nil
		}
		v, _ := f.ConcreteAt(row)
		msg, _ := v.(json.RawMessage)
		return msg
	}

	result := &tempoTrace{TraceID: traceID, Services: []tempoTraceService{}}
	if frame.Meta != nil {
		if custom, ok := frame.Meta.Custom.(map[string]any); ok {
			result.Partial, _ = custom["partial"].(bool)
			result.Message, _ = custom["message"].(string)
		}
	}

	display := make([]TraceSpan, 0, frame.Rows())
	displayOK := !result.Partial && frame.Rows() <= maxTraceSpans
	displaySize := 0
	services := map[string]int{}
	for row := 0; row < frame.Rows(); row++ {
		resourceJSON := string(raw("serviceTags", row))
		resource, err := tempoKeyValues(raw("serviceTags", row))
		if err != nil {
			return nil, nil, fmt.Errorf("parsing resource attributes: %w", err)
		}
		attributes, err := tempoKeyValues(raw("tags", row))
		if err != nil {
			return nil, nil, fmt.Errorf("parsing span attributes: %w", err)
		}
		var logs []struct {
			Timestamp float64         `json:"timestamp"`
			Name      string          `json:"name"`
			Fields    json.RawMessage `json:"fields"`
		}
		if err := unmarshalTempoJSON(raw("logs", row), &logs); err != nil {
			return nil, nil, fmt.Errorf("parsing span events: %w", err)
		}
		var references []struct {
			TraceID string          `json:"traceID"`
			SpanID  string          `json:"spanID"`
			Tags    json.RawMessage `json:"tags"`
		}
		if err := unmarshalTempoJSON(raw("references", row), &references); err != nil {
			return nil, nil, fmt.Errorf("parsing span links: %w", err)
		}

		span := tempoTraceSpan{
			SpanID:        str("spanID", row),
			ParentSpanID:  str("parentSpanID", row),
			Name:          str("operationName", row),
			Kind:          str("kind", row),
			Scope:         str("instrumentationLibraryName", row),
			ScopeVersion:  str("instrumentationLibraryVersion", row),
			StartTimeMs:   num("startTime", row),
			DurationMs:    num("duration", row),
			StatusMessage: str("statusMessage", row),
			TraceState:    str("traceState", row),
		}
		if len(attributes) > 0 {
			span.Attributes = attributes
		}
		code := tracepb.Status_StatusCode(num("statusCode", row))
		switch code {
		case tracepb.Status_STATUS_CODE_OK:
			span.Status = "ok"
		case tracepb.Status_STATUS_CODE_ERROR:
			span.Status = "error"
		}
		for _, l := range logs {
			fields, err := tempoKeyValues(l.Fields)
			if err != nil {
				return nil, nil, fmt.Errorf("parsing span event attributes: %w", err)
			}
			span.Events = append(span.Events, TraceEvent{Name: l.Name, TimeMS: l.Timestamp, Attributes: fields})
		}
		for _, r := range references {
			tags, err := tempoKeyValues(r.Tags)
			if err != nil {
				return nil, nil, fmt.Errorf("parsing span link attributes: %w", err)
			}
			link := tempoTraceLink{TraceID: r.TraceID, SpanID: r.SpanID}
			if len(tags) > 0 {
				link.Attributes = tags
			}
			span.Links = append(span.Links, link)
		}

		serviceName := str("serviceName", row)
		key := serviceName + "\x00" + resourceJSON
		i, ok := services[key]
		if !ok {
			i = len(result.Services)
			services[key] = i
			svc := tempoTraceService{ServiceName: serviceName}
			if len(resource) > 0 {
				svc.Resource = resource
			}
			result.Services = append(result.Services, svc)
		}
		result.Services[i].Spans = append(result.Services[i].Spans, span)

		if !displayOK {
			continue
		}
		displayAttrs := make(map[string]any, len(resource)+len(attributes)+2)
		for k, v := range resource {
			displayAttrs["resource."+k] = v
		}
		for k, v := range attributes {
			displayAttrs[k] = v
		}
		if span.Scope != "" {
			displayAttrs["scope.name"] = span.Scope
		}
		if span.ScopeVersion != "" {
			displayAttrs["scope.version"] = span.ScopeVersion
		}
		events := span.Events
		if events == nil {
			events = []TraceEvent{}
		}
		var parentID *string
		if span.ParentSpanID != "" {
			parentID = &span.ParentSpanID
		}
		d := TraceSpan{
			ID:          span.SpanID,
			ParentID:    parentID,
			Name:        span.Name,
			ServiceName: serviceName,
			StartTimeMS: span.StartTimeMs,
			DurationMS:  span.DurationMs,
			Status:      traceStatus(&tracepb.Status{Code: code}, events),
			Attributes:  displayAttrs,
			Events:      events,
		}
		if addTraceSpanSize(&displaySize, d) != nil {
			displayOK = false
			continue
		}
		display = append(display, d)
	}
	if !displayOK {
		display = nil
	}
	return result, display, nil
}

// tempoKeyValues decodes a frame's [{"key": ..., "value": ...}] list.
func tempoKeyValues(msg json.RawMessage) (map[string]any, error) {
	var kvs []struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if err := unmarshalTempoJSON(msg, &kvs); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		result[kv.Key] = llmValue(kv.Value)
	}
	return result, nil
}

// unmarshalTempoJSON decodes msg into v, keeping numbers exact. Empty and
// null messages leave v untouched.
func unmarshalTempoJSON(msg json.RawMessage, v any) error {
	if len(msg) == 0 || string(msg) == "null" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(msg)))
	dec.UseNumber()
	return dec.Decode(v)
}
