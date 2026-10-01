// Package archtest enforces the dependency rules of docs/architecture/08-engineering-standards.md
// (section 1) and ADR-006/ADR-007/ADR-025. It has no production callers: it exists so CI fails
// when a package imports something its layer must not depend on.
package archtest

import (
	"fmt"
	"sort"
	"strings"
)

// Module is the Go module path of this repository (decision D3).
const Module = "github.com/RedeArmy/flight-intelligence-platform"

// Package is the subset of `go list -json` output the rules need.
type Package struct {
	ImportPath string
	Imports    []string // non-test imports
	AllImports []string // imports including tests
}

// Violation describes one broken rule.
type Violation struct {
	Package string
	Import  string
	Rule    string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s imports %s: %s", v.Package, v.Import, v.Rule)
}

// cloudSDKPrefixes are forbidden everywhere (D1/ADR-025: vendor-neutral, no cloud SDKs).
var cloudSDKPrefixes = []string{
	"github.com/aws/",
	"cloud.google.com/",
	"google.golang.org/api",
	"github.com/Azure/",
	"github.com/azure/",
}

// forbiddenInPureLayers are stdlib packages that couple code to infrastructure.
var forbiddenInPureLayers = []string{"net/http", "database/sql", "os/exec"}

// applicationAllowedExternal lists third-party prefixes the application layer may use.
var applicationAllowedExternal = []string{
	"golang.org/x/sync",
	"go.opentelemetry.io/otel",
}

type layer struct {
	kind    string // domain, application, ports, adapters, platform, shared, connector, cmd, other
	context string // bounded context, or connector name
}

func isStdlib(path string) bool {
	first := path
	if i := strings.Index(path, "/"); i >= 0 {
		first = path[:i]
	}
	return !strings.Contains(first, ".")
}

func trimModule(path string) (string, bool) {
	if path == Module {
		return "", true
	}
	if rest, ok := strings.CutPrefix(path, Module+"/"); ok {
		return rest, true
	}
	return "", false
}

// classify maps an in-module import path to its layer.
func classify(path string) layer {
	rest, ok := trimModule(path)
	if !ok {
		return layer{kind: "external"}
	}
	parts := strings.Split(rest, "/")
	switch {
	case parts[0] == "cmd":
		return layer{kind: "cmd"}
	case parts[0] != "internal" || len(parts) < 2:
		return layer{kind: "other"}
	}
	switch parts[1] {
	case "platform":
		return layer{kind: "platform"}
	case "shared":
		return layer{kind: "shared"}
	case "archtest":
		return layer{kind: "other"}
	case "provider":
		if len(parts) >= 4 && parts[2] == "connectors" {
			return layer{kind: "connector", context: parts[3]}
		}
		return layer{kind: "provider"}
	}
	if len(parts) >= 3 {
		switch parts[2] {
		case "domain", "application", "ports", "adapters":
			return layer{kind: parts[2], context: parts[1]}
		}
	}
	return layer{kind: "other"}
}

// Check evaluates all rules over pkgs and returns violations sorted for stable output.
func Check(pkgs []Package) []Violation {
	var out []Violation
	for _, p := range pkgs {
		from := classify(p.ImportPath)

		// Rule: no cloud SDK anywhere, including tests (D1).
		for _, imp := range p.AllImports {
			for _, prefix := range cloudSDKPrefixes {
				if strings.HasPrefix(imp, prefix) {
					out = append(out, Violation{p.ImportPath, imp, "cloud SDKs are forbidden (D1, ADR-025)"})
				}
			}
		}

		for _, imp := range p.Imports {
			to := classify(imp)
			if v, bad := checkImport(from, to, imp); bad {
				out = append(out, Violation{p.ImportPath, imp, v})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Import < out[j].Import
	})
	return out
}

func checkImport(from, to layer, imp string) (string, bool) {
	std := isStdlib(imp)

	switch from.kind {
	case "domain":
		if std {
			return rejectIfForbiddenStd(imp, "domain must not depend on infrastructure packages")
		}
		if to.kind == "shared" || (to.kind == "domain" && to.context == from.context) {
			return "", false
		}
		return "domain may import only stdlib, internal/shared and its own context's domain (P1, ADR-006)", true

	case "application":
		if std {
			return rejectIfForbiddenStd(imp, "application must not depend on infrastructure packages")
		}
		switch to.kind {
		case "shared":
			return "", false
		case "domain", "ports":
			if to.context == from.context {
				return "", false
			}
			return "application must not import another context's " + to.kind + " (08 rule 4)", true
		case "application":
			// Same-context sub-packages and cross-context calls through the other
			// context's application services are both allowed (08 rule 4).
			return "", false
		case "external":
			for _, allowed := range applicationAllowedExternal {
				if strings.HasPrefix(imp, allowed) {
					return "", false
				}
			}
			return "application may not import third-party packages except " + strings.Join(applicationAllowedExternal, ", "), true
		}
		return "application must not import adapters, platform or provider code (08 rule 2)", true

	case "ports":
		if std {
			return rejectIfForbiddenStd(imp, "ports must not depend on infrastructure packages")
		}
		if to.kind == "shared" || (to.kind == "domain" && to.context == from.context) {
			return "", false
		}
		return "ports may import only stdlib, internal/shared and their own context's domain", true

	case "adapters":
		switch to.kind {
		case "domain", "ports", "application", "adapters":
			if to.context != from.context {
				return "adapters must not import another context's internals (08 rule 3)", true
			}
		}
		return "", false

	case "platform":
		switch to.kind {
		case "domain", "application", "ports", "adapters", "connector":
			return "internal/platform must not depend on bounded contexts or connectors", true
		}
		return "", false

	case "shared":
		if std {
			return "", false
		}
		return "internal/shared may import only the standard library (kernel stays tiny)", true

	case "connector":
		if std || to.kind == "shared" || to.kind == "provider" {
			return "", false
		}
		if to.kind == "connector" {
			if to.context == from.context {
				return "", false
			}
			return "a connector must not import another connector (08 rule 5)", true
		}
		if to.kind == "external" {
			return "", false
		}
		return "connectors may import only internal/provider, internal/shared and third-party packages (canonical types location is decided in E2)", true
	}
	return "", false
}

func rejectIfForbiddenStd(imp, why string) (string, bool) {
	for _, f := range forbiddenInPureLayers {
		if imp == f || strings.HasPrefix(imp, f+"/") {
			return why + " (" + f + ")", true
		}
	}
	return "", false
}
