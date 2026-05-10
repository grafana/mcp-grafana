package rbac

import "testing"

func TestMetrics_NilSafe(t *testing.T) {
	var m *Metrics
	m.CacheHit(nil)
	m.CacheMiss(nil)
	m.FilterObserved(nil, ModeAuto, 0.1)
	defer m.Stopwatch(ModeAuto)() // must not panic
}

func TestNewMetrics_DoesNotPanic(t *testing.T) {
	if NewMetrics() == nil {
		t.Errorf("NewMetrics returned nil")
	}
}
