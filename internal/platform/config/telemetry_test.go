package config

import (
	"testing"
	"time"
)

func TestTelemetryDefaults(t *testing.T) {
	cfg := mustLoad(t, map[string]string{"APP_ENV": "local"})
	if cfg.Telemetry.Endpoint != "" || cfg.Telemetry.SampleRatio != 1 || cfg.Telemetry.MetricInterval != 15*time.Second {
		t.Errorf("defaults = %+v: export must be off by default", cfg.Telemetry)
	}
	cfg = mustLoad(t, map[string]string{"APP_ENV": "local", "TELEMETRY_OTLP_ENDPOINT": "http://localhost:4318", "TELEMETRY_SAMPLE_RATIO": "0.25"})
	if cfg.Telemetry.Endpoint != "http://localhost:4318" || cfg.Telemetry.SampleRatio != 0.25 {
		t.Errorf("explicit = %+v", cfg.Telemetry)
	}
}

func TestTelemetryRules(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"not a URL", map[string]string{"APP_ENV": "local", "TELEMETRY_OTLP_ENDPOINT": "collector:4318"}, "TELEMETRY_OTLP_ENDPOINT"},
		{"wrong scheme", map[string]string{"APP_ENV": "local", "TELEMETRY_OTLP_ENDPOINT": "grpc://collector:4317"}, "TELEMETRY_OTLP_ENDPOINT"},
		{"credentials in URL", map[string]string{"APP_ENV": "local", "TELEMETRY_OTLP_ENDPOINT": "https://user:pw@collector"}, "TELEMETRY_OTLP_ENDPOINT"},
		{"plain http in production", map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full", "TELEMETRY_OTLP_ENDPOINT": "http://collector:4318"}, "TELEMETRY_OTLP_ENDPOINT"},
		{"ratio above one", map[string]string{"APP_ENV": "local", "TELEMETRY_SAMPLE_RATIO": "1.5"}, "TELEMETRY_SAMPLE_RATIO"},
		{"negative ratio", map[string]string{"APP_ENV": "local", "TELEMETRY_SAMPLE_RATIO": "-0.1"}, "TELEMETRY_SAMPLE_RATIO"},
		{"ratio NaN", map[string]string{"APP_ENV": "local", "TELEMETRY_SAMPLE_RATIO": "NaN"}, "TELEMETRY_SAMPLE_RATIO"},
		{"ratio text", map[string]string{"APP_ENV": "local", "TELEMETRY_SAMPLE_RATIO": "half"}, "TELEMETRY_SAMPLE_RATIO"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(mapLookup(tc.env))
			if _, ok := issuesOf(t, err)[tc.key]; !ok {
				t.Fatalf("expected an issue for %s, got %v", tc.key, err)
			}
		})
	}
	prod := map[string]string{"APP_ENV": "production", "POSTGRES_SSLMODE": "verify-full", "TELEMETRY_OTLP_ENDPOINT": "https://collector.example:4318"}
	if _, err := Load(mapLookup(prod)); err != nil {
		t.Errorf("https must be accepted in production: %v", err)
	}
}
