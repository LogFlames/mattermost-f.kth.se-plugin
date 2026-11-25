package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestApplyManifestQuotesEmbeddedJSON(t *testing.T) {
	const description = "Backtick `, interpolation ${value}, newline\n, slash \\, and quote \""
	manifest := &model.Manifest{
		Id:          "test",
		Description: description,
		Server:      &model.ManifestServer{},
		Webapp:      &model.ManifestWebapp{},
	}

	t.Chdir(t.TempDir())
	for _, dir := range []string{"server", "webapp/src"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyManifest(manifest); err != nil {
		t.Fatal(err)
	}

	source, err := os.ReadFile(filepath.Join("server", "manifest.go"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "manifest.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var goJSON string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			if value.Names[0].Name == "manifestStr" {
				goJSON, err = strconv.Unquote(value.Values[0].(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	source, err = os.ReadFile(filepath.Join("webapp", "src", "manifest.ts"))
	if err != nil {
		t.Fatal(err)
	}
	_, literal, found := strings.Cut(string(source), "JSON.parse(")
	if !found {
		t.Fatal("generated TypeScript lacks JSON.parse")
	}
	literal, _, found = strings.Cut(literal, ");")
	if !found {
		t.Fatal("generated TypeScript lacks JSON.parse closing delimiter")
	}
	var jsJSON string
	if err := json.Unmarshal([]byte(literal), &jsJSON); err != nil {
		t.Fatalf("invalid TypeScript string literal: %v", err)
	}

	for name, data := range map[string]string{"Go": goJSON, "TypeScript": jsJSON} {
		var got model.Manifest
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatalf("%s manifest is invalid: %v", name, err)
		}
		if got.Description != description {
			t.Errorf("%s description = %q, want %q", name, got.Description, description)
		}
	}
}
