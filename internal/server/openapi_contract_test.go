package server

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

var openAPIMethodLine = regexp.MustCompile(`^    (get|post|put|delete|patch):`)

type openAPIOperationContract struct {
	method      string
	path        string
	summary     string
	operationID string
	hasTags     bool
	hasResponse bool
	anonymous   bool
}

func TestOpenAPIOperationsHaveStableSDKContracts(t *testing.T) {
	for _, contract := range openAPIContracts() {
		t.Run(contract.name, func(t *testing.T) {
			operations, err := readOpenAPIOperationContracts(contract.document)
			if err != nil {
				t.Fatal(err)
			}
			if len(operations) == 0 {
				t.Fatal("OpenAPI contract contains no operations")
			}

			validID := regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
			seen := make(map[string]string, len(operations))
			for _, operation := range operations {
				label := strings.ToUpper(operation.method) + " " + operation.path
				if operation.summary == "" {
					t.Errorf("%s has no summary", label)
				}
				if !operation.hasTags {
					t.Errorf("%s has no tags", label)
				}
				if !operation.hasResponse {
					t.Errorf("%s has no responses", label)
				}
				if !validID.MatchString(operation.operationID) {
					t.Errorf("%s has invalid operationId %q", label, operation.operationID)
					continue
				}
				want := routeOperationID(operation.method, operation.path)
				if operation.operationID != want {
					t.Errorf("%s operationId = %q, want deterministic %q", label, operation.operationID, want)
				}
				if previous, exists := seen[operation.operationID]; exists {
					t.Errorf("operationId %q is shared by %s and %s", operation.operationID, previous, label)
				} else {
					seen[operation.operationID] = label
				}
			}
		})
	}
}

func TestOpenAPILocalComponentReferencesResolve(t *testing.T) {
	for _, contract := range openAPIContracts() {
		t.Run(contract.name, func(t *testing.T) {
			testOpenAPILocalComponentReferencesResolve(t, contract.document)
		})
	}
}

func testOpenAPILocalComponentReferencesResolve(t *testing.T, document []byte) {
	t.Helper()
	definitions := map[string]bool{}
	inComponents := false
	category := ""
	scanner := bufio.NewScanner(bytes.NewReader(document))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "components:" {
			inComponents = true
			continue
		}
		if !inComponents {
			continue
		}
		if key, ok := yamlKeyAtIndent(line, 2); ok {
			category = key
			continue
		}
		if category != "" {
			if key, ok := yamlKeyAtIndent(line, 4); ok {
				definitions["#/components/"+category+"/"+key] = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	refPattern := regexp.MustCompile(`\$ref:\s*['"]?(#/components/[A-Za-z0-9._/-]+)['"]?`)
	checked := map[string]bool{}
	for _, match := range refPattern.FindAllSubmatch(document, -1) {
		ref := string(match[1])
		if checked[ref] {
			continue
		}
		checked[ref] = true
		if !definitions[ref] {
			t.Errorf("unresolved local OpenAPI reference %q", ref)
		}
	}
	if len(definitions) == 0 {
		t.Fatal("OpenAPI contract contains no reusable components")
	}
}

type openAPIContractFixture struct {
	name     string
	document []byte
}

func openAPIContracts() []openAPIContractFixture {
	return []openAPIContractFixture{
		{name: "library", document: openAPI},
		{name: "multiplayer", document: multiplayerOpenAPI},
	}
}

func readOpenAPIOperationContracts(document []byte) ([]openAPIOperationContract, error) {
	var operations []openAPIOperationContract
	var current *openAPIOperationContract
	path := ""
	inPaths := false
	flush := func() {
		if current != nil {
			operations = append(operations, *current)
			current = nil
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(document))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "paths:" {
			inPaths = true
			continue
		}
		if line == "components:" {
			flush()
			break
		}
		if !inPaths {
			continue
		}
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			flush()
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if match := openAPIMethodLine.FindStringSubmatch(line); len(match) == 2 {
			flush()
			current = &openAPIOperationContract{method: match[1], path: path}
			if strings.Contains(line, "{") {
				current.operationID = inlineYAMLValue(line, "operationId")
				current.summary = inlineYAMLValue(line, "summary")
				current.hasTags = strings.Contains(line, "tags:")
				current.hasResponse = strings.Contains(line, "responses:")
				current.anonymous = strings.Contains(line, "security: []")
			}
			continue
		}
		if current == nil || !strings.HasPrefix(line, "      ") || strings.HasPrefix(line, "        ") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "      operationId:"):
			current.operationID = strings.TrimSpace(strings.TrimPrefix(line, "      operationId:"))
		case strings.HasPrefix(line, "      summary:"):
			current.summary = strings.TrimSpace(strings.TrimPrefix(line, "      summary:"))
		case strings.HasPrefix(line, "      tags:"):
			current.hasTags = true
		case strings.HasPrefix(line, "      responses:"):
			current.hasResponse = true
		case strings.HasPrefix(line, "      security:"):
			current.anonymous = strings.TrimSpace(strings.TrimPrefix(line, "      security:")) == "[]"
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return operations, nil
}

func routeOperationID(method, path string) string {
	value := strings.TrimPrefix(path, "/")
	if value == "" {
		value = "root"
	}
	value = strings.ReplaceAll(value, "{", "by_")
	value = strings.ReplaceAll(value, "}", "")

	var result strings.Builder
	result.WriteString(method)
	upperNext := true
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			upperNext = true
			continue
		}
		if upperNext {
			character = unicode.ToUpper(character)
			upperNext = false
		}
		result.WriteRune(character)
	}
	return result.String()
}

func inlineYAMLValue(line, key string) string {
	marker := key + ":"
	start := strings.Index(line, marker)
	if start < 0 {
		return ""
	}
	value := strings.TrimSpace(line[start+len(marker):])
	if end := strings.Index(value, ","); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func yamlKeyAtIndent(line string, indent int) (string, bool) {
	prefix := strings.Repeat(" ", indent)
	if !strings.HasPrefix(line, prefix) || strings.HasPrefix(line, prefix+" ") {
		return "", false
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "-") {
		return "", false
	}
	separator := strings.Index(trimmed, ":")
	if separator < 1 {
		return "", false
	}
	key := trimmed[:separator]
	if key == "" || strings.ContainsAny(key, " {}[]") {
		return "", false
	}
	return key, true
}

func TestOpenAPIContractIdentityMetadata(t *testing.T) {
	for _, fixture := range openAPIContracts() {
		t.Run(fixture.name, func(t *testing.T) {
			contract := string(fixture.document)
			required := []string{
				"openapi: 3.1.0",
				"identifier: Apache-2.0",
				"jsonSchemaDialect: https://json-schema.org/draft/2020-12/schema",
				"bearerFormat: opaque",
				"x-varkiv-common-response-headers:",
				"X-Varkiv-API-Version: {$ref: '#/components/headers/APIVersion'}",
			}
			if fixture.name == "library" {
				required = append(required,
					"title: Varkiv HTTP API",
					"summary: Stable, versioned contract for the Varkiv library and device ecosystem",
				)
			} else {
				required = append(required,
					"title: Varkiv Multiplayer Coordination API",
					"summary: Stable coordination contract for emulator-aware multiplayer clients",
					"version: v1",
					"X-Varkiv-Multiplayer-Version: {$ref: '#/components/headers/MultiplayerVersion'}",
					"example:",
				)
			}
			for _, value := range required {
				if !strings.Contains(contract, value) {
					t.Errorf("OpenAPI contract is missing %q", value)
				}
			}
			if strings.Contains(contract, "cursor") {
				t.Fatal("OpenAPI contract must not claim cursor pagination while the API uses limit and offset")
			}
			if strings.Contains(contract, "JSONBody") {
				t.Fatal("OpenAPI contract must use explicit request schemas instead of a generic JSON body")
			}
			if fixture.name == "multiplayer" {
				if got := strings.Count(contract, "X-Varkiv-Multiplayer-Version: {$ref: '#/components/headers/MultiplayerVersion'}"); got != 8 {
					t.Errorf("multiplayer version response header references = %d, want 8", got)
				}
				if got := strings.Count(contract, "Cache-Control: {$ref: '#/components/headers/CacheControl'}"); got != 8 {
					t.Errorf("cache-control response header references = %d, want 8", got)
				}
			}
		})
	}
	if got := routeOperationID("get", "/sync/sessions/{id}/operations/{operation_id}"); got != "getSyncSessionsByIdOperationsByOperationId" {
		t.Fatal(fmt.Sprintf("route operation ID normalization drifted: %s", got))
	}
}

func TestOpenAPISecurityBoundariesAreExplicit(t *testing.T) {
	tests := []struct {
		name      string
		document  []byte
		anonymous []string
	}{
		{
			name:     "library",
			document: openAPI,
			anonymous: []string{
				"GET /", "GET /health", "GET /health/live", "GET /health/ready", "GET /capabilities", "GET /openapi.yaml",
				"GET /web-emulation/readiness", "GET /web-emulation/content/{token}/{name}", "GET /web-emulation/saves/{token}",
				"POST /web-emulation/saves/{token}", "GET /web-netplay/readiness", "POST /web-netplay/sessions/join", "POST /pairing-codes/redeem",
			},
		},
		{
			name:      "multiplayer",
			document:  multiplayerOpenAPI,
			anonymous: []string{"GET /", "GET /capabilities", "GET /openapi.yaml", "POST /sessions/{id}/join"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(string(test.document), "\nsecurity:\n  - bearerAuth: []\n") {
				t.Fatal("OpenAPI contract has no global bearer security requirement")
			}
			operations, err := readOpenAPIOperationContracts(test.document)
			if err != nil {
				t.Fatal(err)
			}
			wantAnonymous := make(map[string]bool, len(test.anonymous))
			for _, operation := range test.anonymous {
				wantAnonymous[operation] = true
			}
			for _, operation := range operations {
				label := strings.ToUpper(operation.method) + " " + operation.path
				if operation.anonymous != wantAnonymous[label] {
					t.Errorf("%s anonymous=%t, want %t", label, operation.anonymous, wantAnonymous[label])
				}
				delete(wantAnonymous, label)
			}
			for operation := range wantAnonymous {
				t.Errorf("anonymous operation is missing from contract: %s", operation)
			}
		})
	}
}
