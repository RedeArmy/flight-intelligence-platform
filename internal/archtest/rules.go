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

// Layer kinds.
const (
	kindDomain      = "domain"
	kindApplication = "application"
	kindPorts       = "ports"
	kindAdapters    = "adapters"
	kindPlatform    = "platform"
	kindShared      = "shared"
	kindProvider    = "provider"
	kindConnector   = "connector"
	kindCmd         = "cmd"
	kindExternal    = "external"
	kindOther       = "other"
)

type layer struct {
	kind    string
	context string // bounded context, or connector name
}

func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func trimModule(path string) (string, bool) {
	if path == Module {
		return "", true
	}
	return strings.CutPrefix(path, Module+"/")
}

// classify maps an import path to its layer.
func classify(path string) layer {
	rest, ok := trimModule(path)
	if !ok {
		return layer{kind: kindExternal}
	}
	parts := strings.Split(rest, "/")
	if parts[0] == "cmd" {
		return layer{kind: kindCmd}
	}
	if parts[0] != "internal" || len(parts) < 2 {
		return layer{kind: kindOther}
	}
	return classifyInternal(parts[1:])
}

// classifyInternal classifies the path elements below "internal/".
func classifyInternal(parts []string) layer {
	switch parts[0] {
	case kindPlatform, kindShared:
		return layer{kind: parts[0]}
	case "provider":
		return classifyProvider(parts)
	case "archtest":
		return layer{kind: kindOther}
	}
	return classifyContext(parts)
}

func classifyProvider(parts []string) layer {
	if len(parts) >= 3 && parts[1] == "connectors" {
		return layer{kind: kindConnector, context: parts[2]}
	}
	return layer{kind: kindProvider}
}

func classifyContext(parts []string) layer {
	if len(parts) < 2 {
		return layer{kind: kindOther}
	}
	switch parts[1] {
	case kindDomain, kindApplication, kindPorts, kindAdapters:
		return layer{kind: parts[1], context: parts[0]}
	}
	return layer{kind: kindOther}
}

// Check evaluates all rules over pkgs and returns violations sorted for stable output.
func Check(pkgs []Package) []Violation {
	var out []Violation
	for _, p := range pkgs {
		out = append(out, cloudViolations(p)...)
		out = append(out, layerViolations(p)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Import < out[j].Import
	})
	return out
}

// cloudViolations flags cloud SDK imports anywhere, including tests (D1).
func cloudViolations(p Package) []Violation {
	var out []Violation
	for _, imp := range p.AllImports {
		if hasAnyPrefix(imp, cloudSDKPrefixes) {
			out = append(out, Violation{p.ImportPath, imp, "cloud SDKs are forbidden (D1, ADR-025)"})
		}
	}
	return out
}

// layerViolations applies the per-layer import rules to non-test imports.
func layerViolations(p Package) []Violation {
	from := classify(p.ImportPath)
	var out []Violation
	for _, imp := range p.Imports {
		if why, bad := checkImport(from, classify(imp), imp); bad {
			out = append(out, Violation{p.ImportPath, imp, why})
		}
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// importRule returns a reason and true when importing `imp` (classified as `to`) from `from` is forbidden.
type importRule func(from, to layer, imp string) (string, bool)

var layerRules = map[string]importRule{
	kindDomain:      domainRule,
	kindApplication: applicationRule,
	kindPorts:       portsRule,
	kindAdapters:    adaptersRule,
	kindPlatform:    platformRule,
	kindShared:      sharedRule,
	kindConnector:   connectorRule,
}

func checkImport(from, to layer, imp string) (string, bool) {
	rule, ok := layerRules[from.kind]
	if !ok {
		return "", false
	}
	return rule(from, to, imp)
}

func sameContext(from, to layer, kind string) bool {
	return to.kind == kind && to.context == from.context
}

func domainRule(from, to layer, imp string) (string, bool) {
	if isStdlib(imp) {
		return rejectIfForbiddenStd(imp, "domain must not depend on infrastructure packages")
	}
	if to.kind == kindShared || sameContext(from, to, kindDomain) {
		return "", false
	}
	return "domain may import only stdlib, internal/shared and its own context's domain (P1, ADR-006)", true
}

func portsRule(from, to layer, imp string) (string, bool) {
	if isStdlib(imp) {
		return rejectIfForbiddenStd(imp, "ports must not depend on infrastructure packages")
	}
	if to.kind == kindShared || sameContext(from, to, kindDomain) {
		return "", false
	}
	return "ports may import only stdlib, internal/shared and their own context's domain", true
}

func applicationRule(from, to layer, imp string) (string, bool) {
	if isStdlib(imp) {
		return rejectIfForbiddenStd(imp, "application must not depend on infrastructure packages")
	}
	switch to.kind {
	case kindShared, kindApplication:
		// Cross-context calls go through the other context's application services (08 rule 4).
		return "", false
	case kindDomain, kindPorts:
		if to.context == from.context {
			return "", false
		}
		return "application must not import another context's " + to.kind + " (08 rule 4)", true
	case kindExternal:
		return applicationExternalRule(imp)
	}
	return "application must not import adapters, platform or provider code (08 rule 2)", true
}

func applicationExternalRule(imp string) (string, bool) {
	if hasAnyPrefix(imp, applicationAllowedExternal) {
		return "", false
	}
	return "application may not import third-party packages except " + strings.Join(applicationAllowedExternal, ", "), true
}

func adaptersRule(from, to layer, _ string) (string, bool) {
	switch to.kind {
	case kindDomain, kindPorts, kindApplication, kindAdapters:
		if to.context != from.context {
			return "adapters must not import another context's internals (08 rule 3)", true
		}
	}
	return "", false
}

func platformRule(_, to layer, _ string) (string, bool) {
	switch to.kind {
	case kindDomain, kindApplication, kindPorts, kindAdapters, kindConnector:
		return "internal/platform must not depend on bounded contexts or connectors", true
	}
	return "", false
}

func sharedRule(_, _ layer, imp string) (string, bool) {
	if isStdlib(imp) {
		return "", false
	}
	return "internal/shared may import only the standard library (kernel stays tiny)", true
}

func connectorRule(from, to layer, imp string) (string, bool) {
	if isStdlib(imp) {
		return "", false
	}
	switch to.kind {
	case kindShared, kindProvider, kindExternal:
		return "", false
	case kindConnector:
		if to.context == from.context {
			return "", false
		}
		return "a connector must not import another connector (08 rule 5)", true
	}
	return "connectors may import only internal/provider, internal/shared and third-party packages (canonical types location is decided in E2)", true
}

func rejectIfForbiddenStd(imp, why string) (string, bool) {
	for _, f := range forbiddenInPureLayers {
		if imp == f || strings.HasPrefix(imp, f+"/") {
			return why + " (" + f + ")", true
		}
	}
	return "", false
}
