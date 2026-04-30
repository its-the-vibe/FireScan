package main

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	content := `
project_id: "test-project"
credentials_file: ""
batch_size: 10
port: 9090
collections:
  - col1
  - col2
`
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := loadConfig(f.Name()); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg.ProjectID != "test-project" {
		t.Errorf("expected project_id 'test-project', got %q", cfg.ProjectID)
	}
	if cfg.BatchSize != 10 {
		t.Errorf("expected batch_size 10, got %d", cfg.BatchSize)
	}
	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
	if len(cfg.Collections) != 2 {
		t.Errorf("expected 2 collections, got %d", len(cfg.Collections))
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	content := `project_id: "proj"`
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := loadConfig(f.Name()); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg.BatchSize != 25 {
		t.Errorf("expected default batch_size 25, got %d", cfg.BatchSize)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Port)
	}
}

func TestTemplatesParse(t *testing.T) {
	tmpl, err := template.New("").ParseGlob("templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	// Test index template renders without error
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", indexData{
		ProjectID: "test-project",
		Collections: []collectionInfo{
			{Name: "users", Count: 42},
		},
	}); err != nil {
		t.Fatalf("index.html template execution failed: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("index.html rendered empty output")
	}

	// Test collection template renders without error
	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "collection.html", collectionData{
		Collection: "users",
		Page:       1,
		TotalPages: 75,
		Total:      75,
		HasPrev:    false,
		HasNext:    true,
		Docs: []docInfo{
			{ID: "abc123", JSON: `{"name": "Alice"}`, Timestamp: "2024-01-01T00:00:00Z"},
		},
		BatchStart: 1,
		CurrentDoc: docInfo{ID: "abc123", JSON: `{"name": "Alice"}`, Timestamp: "2024-01-01T00:00:00Z"},
		DocsJSON:   template.JS(`[{"ID":"abc123","JSON":"{\"name\": \"Alice\"}","Timestamp":"2024-01-01T00:00:00Z"}]`),
	}); err != nil {
		t.Fatalf("collection.html template execution failed: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("collection.html rendered empty output")
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	err := loadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(": invalid: yaml: {{{"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := loadConfig(f.Name()); err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestIndexHandlerNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/other/path", nil)
	w := httptest.NewRecorder()
	indexHandler(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestCollectionHandlerEmptyName(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/collection/", nil)
	w := httptest.NewRecorder()
	collectionHandler(w, req)
	if w.Code != http.StatusFound {
		t.Errorf("expected redirect 302, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
}

func TestRenderTemplate(t *testing.T) {
	tmpl, err := template.New("").ParseGlob("templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}
	templates = tmpl

	w := httptest.NewRecorder()
	renderTemplate(w, "index.html", indexData{ProjectID: "test"})
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected content-type text/html; charset=utf-8, got %q", ct)
	}
}

func TestRenderTemplateInvalidTemplate(t *testing.T) {
	tmpl, err := template.New("").ParseGlob("templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}
	templates = tmpl

	w := httptest.NewRecorder()
	renderTemplate(w, "nonexistent.html", nil)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for unknown template, got %d", w.Code)
	}
}
