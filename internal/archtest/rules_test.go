package archtest

import (
	"strings"
	"testing"
)

func p(path string, imports ...string) Package {
	return Package{ImportPath: Module + "/" + path, Imports: imports, AllImports: imports}
}

func mod(path string) string { return Module + "/" + path }

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		pkg  Package
		want string // substring of the expected violation rule, empty = no violation
	}{
		{"domain stdlib ok", p("internal/shopping/domain", "fmt", "time", "errors"), ""},
		{"domain shared ok", p("internal/shopping/domain", mod("internal/shared/money")), ""},
		{"domain own domain ok", p("internal/shopping/domain/offer", mod("internal/shopping/domain")), ""},
		{"domain net/http", p("internal/shopping/domain", "net/http"), "infrastructure"},
		{"domain database/sql", p("internal/history/domain", "database/sql"), "infrastructure"},
		{"domain third party", p("internal/shopping/domain", "github.com/jackc/pgx/v5"), "domain may import only"},
		{"domain other context", p("internal/shopping/domain", mod("internal/history/domain")), "domain may import only"},
		{"domain imports platform", p("internal/shopping/domain", mod("internal/platform/database")), "domain may import only"},

		{"application own domain ok", p("internal/shopping/application", mod("internal/shopping/domain"), mod("internal/shopping/ports")), ""},
		{"application x/sync ok", p("internal/shopping/application", "golang.org/x/sync/errgroup"), ""},
		{"application otel ok", p("internal/shopping/application", "go.opentelemetry.io/otel/trace"), ""},
		{"application other app ok", p("internal/monitoring/application", mod("internal/shopping/application")), ""},
		{"application adapters", p("internal/shopping/application", mod("internal/shopping/adapters")), "adapters, platform"},
		{"application platform", p("internal/shopping/application", mod("internal/platform/cache")), "adapters, platform"},
		{"application provider", p("internal/shopping/application", mod("internal/provider/gateway")), "adapters, platform"},
		{"application other domain", p("internal/monitoring/application", mod("internal/shopping/domain")), "another context"},
		{"application third party", p("internal/shopping/application", "github.com/go-chi/chi/v5"), "third-party"},
		{"application net/http", p("internal/shopping/application", "net/http"), "infrastructure"},

		{"ports own domain ok", p("internal/shopping/ports", mod("internal/shopping/domain"), "context"), ""},
		{"ports adapters", p("internal/shopping/ports", mod("internal/shopping/adapters")), "ports may import only"},

		{"adapter platform ok", p("internal/shopping/adapters/postgres", mod("internal/platform/database"), mod("internal/shopping/ports")), ""},
		{"adapter other context", p("internal/shopping/adapters/postgres", mod("internal/history/domain")), "another context"},

		{"platform ok", p("internal/platform/database", mod("internal/shared/errors"), "github.com/jackc/pgx/v5"), ""},
		{"platform imports context", p("internal/platform/database", mod("internal/shopping/domain")), "must not depend on bounded contexts"},

		{"shared stdlib ok", p("internal/shared/money", "fmt"), ""},
		{"shared third party", p("internal/shared/money", "github.com/shopspring/decimal"), "standard library"},

		{"connector provider ok", p("internal/provider/connectors/mock", mod("internal/provider/resilience"), mod("internal/shared/money")), ""},
		{"connector own package ok", p("internal/provider/connectors/mock/mapper", mod("internal/provider/connectors/mock")), ""},
		{"connector other connector", p("internal/provider/connectors/mock", mod("internal/provider/connectors/airlinex")), "another connector"},
		{"connector imports domain", p("internal/provider/connectors/mock", mod("internal/shopping/domain")), "connectors may import only"},

		{"cmd unrestricted", p("cmd/api", mod("internal/platform/httpserver"), mod("internal/shopping/application"), "net/http"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertViolation(t, Check([]Package{tc.pkg}), tc.want)
		})
	}
}

// assertViolation checks that got is empty when want is empty, otherwise that some violation's rule contains want.
func assertViolation(t *testing.T, got []Violation, want string) {
	t.Helper()
	if want == "" {
		if len(got) != 0 {
			t.Fatalf("unexpected violations: %v", got)
		}
		return
	}
	if !anyRuleContains(got, want) {
		t.Fatalf("expected a violation containing %q, got %v", want, got)
	}
}

func anyRuleContains(vs []Violation, substr string) bool {
	for _, v := range vs {
		if strings.Contains(v.Rule, substr) {
			return true
		}
	}
	return false
}

func TestCloudSDKForbiddenEverywhere(t *testing.T) {
	pkg := Package{
		ImportPath: Module + "/cmd/api",
		Imports:    []string{"fmt"},
		AllImports: []string{"fmt", "github.com/aws/aws-sdk-go-v2/config"},
	}
	got := Check([]Package{pkg})
	if len(got) != 1 || !strings.Contains(got[0].Rule, "cloud SDKs") {
		t.Fatalf("want one cloud SDK violation, got %v", got)
	}
}

func TestCheckIsDeterministic(t *testing.T) {
	pkgs := []Package{
		p("internal/shopping/domain", "net/http", "database/sql"),
		p("internal/history/domain", "net/http"),
	}
	a, b := Check(pkgs), Check(pkgs)
	if len(a) != len(b) || len(a) != 3 {
		t.Fatalf("want 3 stable violations, got %d and %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic order at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestIsStdlib(t *testing.T) {
	for path, want := range map[string]bool{
		"fmt": true, "net/http": true, "encoding/json": true,
		"github.com/x/y": false, "golang.org/x/sync": false, "go.opentelemetry.io/otel": false,
	} {
		if got := isStdlib(path); got != want {
			t.Errorf("isStdlib(%q) = %v, want %v", path, got, want)
		}
	}
}
