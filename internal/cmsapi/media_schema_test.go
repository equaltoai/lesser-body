package cmsapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Schema-validated selection-set conformance for the media queries (issue
// #589). gqlgen validates selection sets against lesser's GraphQL schema
// PRE-RESOLVER: selecting a field that does not exist on the operation's return
// type makes the whole operation fail with HTTP 422 before any resolver runs.
// The mock-based tool tests could not catch this because their fixtures echo
// whatever fields the client selects — so the selection sets built by the
// field-builder helpers are validated here against a pinned excerpt of the real
// lesser schema (testdata/lesser_media_schema.graphql) with a field-existence
// resolver that mirrors gqlgen's check. A mock fixture can no longer hide an
// invalid selection set.

// sdlSchema is the minimal schema model the conformance validator needs:
// object types and their field → return-type maps, plus the scalar/enum/input
// names used for variable-type checks. Nullability and list wrappers are
// stripped during resolution and never participate in existence checks.
type sdlSchema struct {
	types   map[string]map[string]string
	scalars map[string]bool
	enums   map[string]bool
	inputs  map[string]bool
}

// docField is one selected field; sel is its nested selection set, nil for
// leaf fields.
type docField struct {
	name string
	sel  []docField
}

type graphQLDocument struct {
	kind     string // "query" or "mutation"
	varTypes []string
	root     []docField
}

func loadMediaSchemaFixture(t *testing.T) *sdlSchema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "lesser_media_schema.graphql"))
	if err != nil {
		t.Fatalf("read media schema fixture: %v", err)
	}
	tokens, err := tokenizeGraphQL(string(raw))
	if err != nil {
		t.Fatalf("tokenize media schema fixture: %v", err)
	}
	schema, err := parseSDL(tokens)
	if err != nil {
		t.Fatalf("parse media schema fixture: %v", err)
	}
	return schema
}

// tokenizeGraphQL splits a GraphQL document into tokens. Type tokens (including
// list/bang wrappers and variable references) are kept whole; punctuation that
// matters for selection-set resolution ({ } ( ) : =) becomes single-character
// tokens; comments (#... and """...""") and quoted strings are skipped.
func tokenizeGraphQL(src string) ([]string, error) {
	var tokens []string
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '{' || c == '}' || c == '(' || c == ')' || c == ':' || c == '=':
			tokens = append(tokens, string(c))
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '"':
			if strings.HasPrefix(src[i:], `"""`) {
				end := strings.Index(src[i+3:], `"""`)
				if end < 0 {
					return nil, fmt.Errorf("unterminated block comment at byte %d", i)
				}
				i += 3 + end + 3
			} else {
				end := strings.IndexByte(src[i+1:], '"')
				if end < 0 {
					return nil, fmt.Errorf("unterminated string at byte %d", i)
				}
				i += 1 + end + 1
			}
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\n\r,{}():=", rune(src[j])) {
				j++
			}
			tokens = append(tokens, src[i:j])
			i = j
		}
	}
	return tokens, nil
}

// parseSDL parses the fixture's definition subset: scalar/enum/input/type
// definitions with plain `field: Type` (or `field(args): Type`) members.
func parseSDL(tokens []string) (*sdlSchema, error) {
	s := &sdlSchema{
		types:   map[string]map[string]string{},
		scalars: map[string]bool{},
		enums:   map[string]bool{},
		inputs:  map[string]bool{},
	}
	i := 0
	for i < len(tokens) {
		switch tokens[i] {
		case "scalar":
			if i+2 > len(tokens) {
				return nil, fmt.Errorf("scalar without name at token %d", i)
			}
			s.scalars[tokens[i+1]] = true
			i += 2
		case "enum", "input":
			kind := tokens[i]
			if i+2 > len(tokens) || tokens[i+2] != "{" {
				return nil, fmt.Errorf("%s %q: want {", kind, tokens[i+1])
			}
			name := tokens[i+1]
			if kind == "enum" {
				s.enums[name] = true
			} else {
				s.inputs[name] = true
			}
			i += 3
			for tokens[i] != "}" {
				i++
			}
			i++
		case "type":
			if i+2 > len(tokens) || tokens[i+2] != "{" {
				return nil, fmt.Errorf("type %q: want {", tokens[i+1])
			}
			name := tokens[i+1]
			i += 3
			fields := map[string]string{}
			for tokens[i] != "}" {
				fname, ftype, next, err := parseFieldDef(tokens, i)
				if err != nil {
					return nil, fmt.Errorf("type %s: %w", name, err)
				}
				fields[fname] = ftype
				i = next
			}
			i++
			s.types[name] = fields
		default:
			return nil, fmt.Errorf("unexpected SDL token %q at token %d", tokens[i], i)
		}
	}
	return s, nil
}

// parseFieldDef parses `name(args): Type` starting at index i and returns the
// field name, its return type token, and the index after the type.
func parseFieldDef(tokens []string, i int) (string, string, int, error) {
	name := tokens[i]
	i++
	if i < len(tokens) && tokens[i] == "(" {
		depth := 0
		for {
			if i >= len(tokens) {
				return "", "", 0, fmt.Errorf("field %s: unterminated argument list", name)
			}
			switch tokens[i] {
			case "(":
				depth++
			case ")":
				depth--
			}
			i++
			if depth == 0 {
				break
			}
		}
	}
	if i >= len(tokens) || tokens[i] != ":" {
		return "", "", 0, fmt.Errorf("field %s: want ':'", name)
	}
	i++
	if i >= len(tokens) {
		return "", "", 0, fmt.Errorf("field %s: missing return type", name)
	}
	return name, tokens[i], i + 1, nil
}

// parseGraphQLDocument parses one operation (the client-built query/mutation
// templates) into a document whose root selection set can be validated.
func parseGraphQLDocument(src string) (*graphQLDocument, error) {
	tokens, err := tokenizeGraphQL(src)
	if err != nil {
		return nil, err
	}
	doc := &graphQLDocument{}
	i := 0
	if tokens[i] == "query" || tokens[i] == "mutation" {
		doc.kind = tokens[i]
		i++
		// Optional operation name.
		if i < len(tokens) && tokens[i] != "(" && tokens[i] != "{" {
			i++
		}
		// Variable definitions.
		if i < len(tokens) && tokens[i] == "(" {
			i++
			for i < len(tokens) && tokens[i] != ")" {
				if tokens[i] != ":" {
					// $name
					i++
					continue
				}
				i++
				doc.varTypes = append(doc.varTypes, tokens[i])
				i++
				if i < len(tokens) && tokens[i] == "=" {
					i += 2 // default value
				}
			}
			if i >= len(tokens) {
				return nil, fmt.Errorf("unterminated variable definitions")
			}
			i++ // )
		}
	}
	if i >= len(tokens) || tokens[i] != "{" {
		return nil, fmt.Errorf("operation has no root selection set")
	}
	root, next, err := parseSelectionSet(tokens, i+1)
	if err != nil {
		return nil, err
	}
	if next != len(tokens) {
		return nil, fmt.Errorf("trailing tokens after operation")
	}
	doc.root = root
	return doc, nil
}

// parseSelectionSet parses `field { ... }` entries starting at index i (which
// must be the token after the opening brace) until the matching closing brace.
func parseSelectionSet(tokens []string, i int) ([]docField, int, error) {
	var fields []docField
	for i < len(tokens) && tokens[i] != "}" {
		name := tokens[i]
		i++
		// Skip arguments.
		if i < len(tokens) && tokens[i] == "(" {
			depth := 0
			for {
				if i >= len(tokens) {
					return nil, 0, fmt.Errorf("field %s: unterminated arguments", name)
				}
				switch tokens[i] {
				case "(":
					depth++
				case ")":
					depth--
				}
				i++
				if depth == 0 {
					break
				}
			}
		}
		var sel []docField
		if i < len(tokens) && tokens[i] == "{" {
			var err error
			sel, i, err = parseSelectionSet(tokens, i+1)
			if err != nil {
				return nil, 0, err
			}
		}
		fields = append(fields, docField{name: name, sel: sel})
	}
	if i >= len(tokens) {
		return nil, 0, fmt.Errorf("unterminated selection set")
	}
	return fields, i + 1, nil
}

// innermostTypeName strips list and non-null wrappers: "[EditorialMediaUsage!]!"
// → "EditorialMediaUsage", "Draft!" → "Draft", "Boolean" → "Boolean".
func innermostTypeName(t string) string {
	t = strings.TrimPrefix(t, "[")
	for strings.HasSuffix(t, "]") || strings.HasSuffix(t, "!") {
		t = t[:len(t)-1]
	}
	return t
}

func knownType(t string, s *sdlSchema) bool {
	inner := innermostTypeName(t)
	return s.scalars[inner] || s.enums[inner] || s.inputs[inner] || s.types[inner] != nil
}

// validateDocument runs the pre-resolver existence check gqlgen applies: every
// selected field must exist on the resolved type of its parent, and variable
// types must be defined. Returns a slice of gqlgen-style messages, empty when
// the document is schema-valid.
func validateDocument(doc *graphQLDocument, s *sdlSchema) []string {
	var errs []string
	for _, vt := range doc.varTypes {
		if !knownType(vt, s) {
			errs = append(errs, fmt.Sprintf("Variable type %q is not defined in the schema", vt))
		}
	}
	rootType := "Query"
	if doc.kind == "mutation" {
		rootType = "Mutation"
	}
	errs = append(errs, validateSelectionSet(doc.root, rootType, s)...)
	return errs
}

func validateSelectionSet(sel []docField, typeName string, s *sdlSchema) []string {
	var errs []string
	fields, ok := s.types[typeName]
	if !ok {
		errs = append(errs, fmt.Sprintf("Type %q is not defined in the schema", typeName))
		return errs
	}
	for _, f := range sel {
		ftype, ok := fields[f.name]
		if !ok {
			errs = append(errs, fmt.Sprintf("Field %q doesn't exist on type %q", f.name, typeName))
			continue
		}
		if len(f.sel) > 0 {
			errs = append(errs, validateSelectionSet(f.sel, innermostTypeName(ftype), s)...)
		}
	}
	return errs
}

// TestMediaSelectionSetsValidateAgainstLesserSchema validates the exact
// documents the cmsapi client sends (assembled by the operation seam functions
// and the field builders) against the real lesser schema. The draftReview read
// returns DraftReview (draftId + review-state fields); the setDraftEditorialMedia
// mutation returns Draft! (id, no review-state fields). Both must validate —
// gqlgen rejects anything else with HTTP 422 pre-resolver.
func TestMediaSelectionSetsValidateAgainstLesserSchema(t *testing.T) {
	schema := loadMediaSchemaFixture(t)
	for _, tc := range []struct {
		name  string
		query string
	}{
		{
			name:  "BodyDraftEditorialMedia read (draftReview returns DraftReview)",
			query: buildDraftEditorialMediaOperation("draft-1").Query,
		},
		{
			name:  "BodySetDraftEditorialMedia mutation (setDraftEditorialMedia returns Draft!)",
			query: buildSetDraftEditorialMediaOperation("draft-1", nil).Query,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parseGraphQLDocument(tc.query)
			if err != nil {
				t.Fatalf("parse query: %v\n%s", err, tc.query)
			}
			errs := validateDocument(doc, schema)
			if len(errs) > 0 {
				t.Fatalf("query is schema-invalid (gqlgen would reject it with HTTP 422 pre-resolver):\n%s\n%s",
					tc.query, strings.Join(errs, "\n"))
			}
		})
	}
}

// TestDraftReviewProjectionIsRejectedOnDraft pins the issue #589 defect: the
// pre-fix selection set shared draftMediaStateFields() — draftId,
// activeReviewerIds, verdicts, publishEligibility — which exist only on
// DraftReview, with the setDraftEditorialMedia mutation whose return type is
// Draft!. The validator must reject exactly those fields, mirroring the gqlgen
// pre-resolver check that 422'd every draft_media_attach/detach/reorder call.
// If this test ever stops failing, the fixture or the validator no longer
// models the real schema.
func TestDraftReviewProjectionIsRejectedOnDraft(t *testing.T) {
	schema := loadMediaSchemaFixture(t)
	preFixQuery := fmt.Sprintf(bodySetDraftEditorialMediaQueryTemplate, draftMediaStateFields())
	doc, err := parseGraphQLDocument(preFixQuery)
	if err != nil {
		t.Fatalf("parse pre-fix query: %v", err)
	}
	errs := validateDocument(doc, schema)
	if len(errs) == 0 {
		t.Fatalf("pre-fix selection set validated cleanly against Draft — the schema conformance guard no longer models the real schema")
	}
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		`Field "draftId" doesn't exist on type "Draft"`,
		`Field "activeReviewerIds" doesn't exist on type "Draft"`,
		`Field "verdicts" doesn't exist on type "Draft"`,
		`Field "publishEligibility" doesn't exist on type "Draft"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected rejection %q, got:\n%s", want, joined)
		}
	}
}

// TestMediaSchemaFixtureIsInternallyConsistent guards the fixture itself: every
// field return type must resolve to a defined type, and the media root fields
// and both projection types must exist. A silently permissive fixture would
// defeat the conformance guard.
func TestMediaSchemaFixtureIsInternallyConsistent(t *testing.T) {
	schema := loadMediaSchemaFixture(t)
	for typeName, fields := range schema.types {
		for fname, ftype := range fields {
			if !knownType(ftype, schema) {
				t.Errorf("fixture type %s field %s references undefined type %q", typeName, fname, ftype)
			}
		}
	}
	for _, want := range []string{"Draft", "DraftReview", "Query", "Mutation", "EditorialMediaUsage"} {
		if schema.types[want] == nil {
			t.Errorf("fixture is missing type %q", want)
		}
	}
}
