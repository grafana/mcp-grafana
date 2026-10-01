package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Tempo's LLM representation is experimental. Decode only complete supported
// traces; callers keep the original response when enrichment is unavailable.
func decodeDisplayTrace(body string) ([]TraceSpan, error) {
	var envelope struct {
		Trace  json.RawMessage `json:"trace"`
		Status string          `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return nil, err
	}
	if strings.EqualFold(envelope.Status, "partial") {
		return nil, fmt.Errorf("partial trace")
	}
	var format struct {
		Services json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(envelope.Trace, &format); err != nil {
		return nil, err
	}
	if len(format.Services) == 0 {
		trace, err := decodeTraceResponse(strings.NewReader(body), defaultResponseLimitBytes)
		if err != nil {
			return nil, err
		}
		return normalizeTrace(trace)
	}
	var trace llmTrace
	decoder := json.NewDecoder(strings.NewReader(string(envelope.Trace)))
	decoder.UseNumber()
	if err := decoder.Decode(&trace); err != nil {
		return nil, err
	}
	spans := []TraceSpan{}
	size := 0
	for _, service := range trace.Services {
		for _, scope := range service.Scopes {
			for _, span := range scope.Spans {
				if len(spans) >= maxTraceSpans {
					return nil, fmt.Errorf("trace exceeds span limit")
				}
				if !isHexID(span.SpanID, 16) {
					return nil, fmt.Errorf("invalid span ID")
				}
				start, err := strconv.ParseUint(string(span.Start), 10, 64)
				if err != nil {
					return nil, err
				}
				end, err := strconv.ParseUint(string(span.End), 10, 64)
				if err != nil || end < start {
					return nil, fmt.Errorf("invalid span end time")
				}
				attrs := map[string]any{}
				for k, v := range service.Resource {
					attrs["resource."+k] = llmValue(v)
				}
				for k, v := range span.Attributes {
					attrs[k] = llmValue(v)
				}
				if scope.Name != "" {
					attrs["scope.name"] = scope.Name
				}
				if scope.Version != "" {
					attrs["scope.version"] = scope.Version
				}
				events := make([]TraceEvent, 0, len(span.Events))
				for _, event := range span.Events {
					timestamp, err := strconv.ParseUint(string(event.Time), 10, 64)
					if err != nil {
						return nil, err
					}
					eventAttrs := map[string]any{}
					for k, v := range event.Attributes {
						eventAttrs[k] = llmValue(v)
					}
					events = append(events, TraceEvent{Name: event.Name, TimeMS: float64(timestamp) / 1e6, Attributes: eventAttrs})
				}
				code, ok := tracepb.Status_StatusCode_value[span.Status.Code]
				if span.Status.Code != "" && !ok {
					return nil, fmt.Errorf("unsupported span status")
				}
				var parent *string
				if span.ParentID != "" {
					if !isHexID(span.ParentID, 16) {
						return nil, fmt.Errorf("invalid parent span ID")
					}
					id := strings.ToLower(span.ParentID)
					parent = &id
				}
				normalized := TraceSpan{ID: strings.ToLower(span.SpanID), ParentID: parent, Name: span.Name, ServiceName: service.Name, StartTimeMS: float64(start) / 1e6, DurationMS: float64(end-start) / 1e6, Status: traceStatus(&tracepb.Status{Code: tracepb.Status_StatusCode(code)}, events), Attributes: attrs, Events: events}
				if err := addTraceSpanSize(&size, normalized); err != nil {
					return nil, err
				}
				spans = append(spans, normalized)
			}
		}
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("trace has no spans")
	}
	return spans, nil
}

type llmTrace struct {
	Services []struct {
		Name     string         `json:"serviceName"`
		Resource map[string]any `json:"resource"`
		Scopes   []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Spans   []struct {
				SpanID     string         `json:"spanId"`
				ParentID   string         `json:"parentSpanId"`
				Name       string         `json:"name"`
				Start      json.Number    `json:"startTimeUnixNano"`
				End        json.Number    `json:"endTimeUnixNano"`
				Attributes map[string]any `json:"attributes"`
				Status     struct {
					Code string `json:"code"`
				} `json:"status"`
				Events []struct {
					Name       string         `json:"name"`
					Time       json.Number    `json:"timeUnixNano"`
					Attributes map[string]any `json:"attributes"`
				} `json:"events"`
			} `json:"spans"`
		} `json:"scopes"`
	} `json:"services"`
}

func llmValue(value any) any {
	switch v := value.(type) {
	case json.Number:
		if integer, err := v.Int64(); err == nil {
			if integer > 1<<53-1 || integer < -(1<<53-1) {
				return string(v)
			}
			return integer
		}
		if !strings.ContainsAny(string(v), ".eE") {
			return string(v)
		}
		number, err := v.Float64()
		if err != nil {
			return string(v)
		}
		return number
	case []any:
		for i, item := range v {
			v[i] = llmValue(item)
		}
		return v
	case map[string]any:
		for k, item := range v {
			v[k] = llmValue(item)
		}
		return v
	default:
		return value
	}
}
