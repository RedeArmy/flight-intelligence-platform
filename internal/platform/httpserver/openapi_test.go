package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/RedeArmy/flight-intelligence-platform/internal/platform/access"
)

const specPath = "../../../api/openapi/v1/openapi.yaml"

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(filepath.FromSlash(specPath))
	if err != nil {
		t.Fatalf("loading the OpenAPI contract: %v", err)
	}
	return doc
}

// specOperation is one method and path of the contract with its effective access setting.
type specOperation struct {
	key        string
	public     bool
	permission string
}

func specOperations(t *testing.T, doc *openapi3.T) []specOperation {
	t.Helper()
	var ops []specOperation
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			security := doc.Security
			if op.Security != nil {
				security = *op.Security
			}
			ops = append(ops, specOperation{
				key:        method + " " + path,
				public:     len(security) == 0,
				permission: extensionString(op.Extensions["x-permission"]),
			})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].key < ops[j].key })
	return ops
}

// extensionString reads a string extension whether the loader decoded it as a string or kept raw JSON.
func extensionString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(x, &s); err == nil {
			return s
		}
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func TestOpenAPIContractIsValid(t *testing.T) {
	if err := loadSpec(t).Validate(context.Background()); err != nil {
		t.Fatalf("the OpenAPI contract is invalid: %v", err)
	}
}

func TestEveryOperationDeclaresItsAccessAndMatchesTheRoutePolicies(t *testing.T) {
	doc := loadSpec(t)
	ops := specOperations(t, doc)
	if len(ops) == 0 {
		t.Fatal("the contract has no operations; the test is not checking anything")
	}

	inSpec := map[string]bool{}
	for _, op := range ops {
		inSpec[op.key] = true
		checkOperationAgainstPolicy(t, op)
	}
	for key := range routePolicies {
		if !inSpec[key] {
			t.Errorf("route policy %q has no operation in the contract", key)
		}
	}
}

// checkOperationAgainstPolicy verifies one contract operation against its route policy.
func checkOperationAgainstPolicy(t *testing.T, op specOperation) {
	t.Helper()
	policy, ok := routePolicies[op.key]
	if !ok {
		t.Errorf("%s is in the contract but has no route policy", op.key)
		return
	}
	if policy.public != op.public {
		t.Errorf("%s: contract says public=%v, policy says public=%v", op.key, op.public, policy.public)
	}
	if op.public {
		if op.permission != "" {
			t.Errorf("%s is public but declares x-permission %q", op.key, op.permission)
		}
		return
	}
	if op.permission == "" || access.Permission(op.permission) != policy.permission {
		t.Errorf("%s: x-permission %q does not match policy permission %q", op.key, op.permission, policy.permission)
	}
	if !someRoleCan(policy.permission) {
		t.Errorf("%s: no role grants permission %q, so nobody could call it", op.key, policy.permission)
	}
}

func someRoleCan(p access.Permission) bool {
	for _, role := range []access.Role{access.RoleUser, access.RoleDeveloper, access.RoleOperator, access.RoleAdmin, access.RoleService} {
		if role.Can(p) {
			return true
		}
	}
	return false
}

func TestRegisteredRoutesAreExactlyTheContract(t *testing.T) {
	handler := publicHandler(newTestLog(t), nil, nil)
	mux, ok := handler.(*chi.Mux)
	if !ok {
		t.Fatalf("public handler is %T, want *chi.Mux", handler)
	}
	registered := map[string]bool{}
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{}
	for _, op := range specOperations(t, loadSpec(t)) {
		want[op.key] = true
	}
	if !reflect.DeepEqual(registered, want) {
		t.Fatalf("registered routes differ from the contract\n registered: %v\n contract:   %v", keys(registered), keys(want))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestErrorEnvelopeMatchesTheContract keeps the hand-written envelope in step with the ErrorResponse schema.
func TestErrorEnvelopeMatchesTheContract(t *testing.T) {
	doc := loadSpec(t)
	schema := doc.Components.Schemas["Error"].Value
	if schema == nil {
		t.Fatal("the contract has no Error schema")
	}
	var contractProps []string
	for name := range schema.Properties {
		contractProps = append(contractProps, name)
	}
	sort.Strings(contractProps)

	var codeProps []string
	typ := reflect.TypeOf(errorPayload{})
	for i := range typ.NumField() {
		codeProps = append(codeProps, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(codeProps)
	if !reflect.DeepEqual(contractProps, codeProps) {
		t.Errorf("Error properties differ: contract %v, code %v", contractProps, codeProps)
	}

	required := append([]string(nil), schema.Required...)
	sort.Strings(required)
	if want := []string{"code", "message", "requestId"}; !reflect.DeepEqual(required, want) {
		t.Errorf("required fields = %v, want %v", required, want)
	}
}

// TestEveryDocumentedErrorStatusIsProducible checks the error responses the contract promises are real statuses.
func TestEveryDocumentedErrorStatusIsProducible(t *testing.T) {
	producible := map[string]bool{}
	for _, status := range []int{
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusConflict, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout,
	} {
		producible[fmt.Sprint(status)] = true
	}
	for path, item := range loadSpec(t).Paths.Map() {
		for method, op := range item.Operations() {
			for code := range op.Responses.Map() {
				if strings.HasPrefix(code, "2") {
					continue
				}
				if !producible[code] {
					t.Errorf("%s %s documents status %s which the server never produces", method, path, code)
				}
			}
		}
	}
}
