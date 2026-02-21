package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	firestorepb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"gopkg.in/yaml.v3"
)

// Config holds the application configuration loaded from config.yaml.
type Config struct {
	ProjectID       string   `yaml:"project_id"`
	CredentialsFile string   `yaml:"credentials_file"`
	BatchSize       int      `yaml:"batch_size"`
	Port            int      `yaml:"port"`
	Collections     []string `yaml:"collections"`
}

// collectionInfo is used to render the index page.
type collectionInfo struct {
	Name  string
	Count int
}

// docInfo represents a single Firestore document for rendering.
type docInfo struct {
	ID        string
	JSON      string
	Timestamp string
}

// indexData is passed to the index template.
type indexData struct {
	ProjectID   string
	Collections []collectionInfo
}

// collectionData is passed to the collection template.
type collectionData struct {
	Collection string
	Page       int          // current record number (1-based)
	TotalPages int          // total records (same as Total; kept for compatibility)
	Total      int          // total documents in the collection
	HasPrev    bool
	HasNext    bool
	Docs       []docInfo    // full preloaded batch for client-side navigation
	BatchStart int          // 1-based record number of the first doc in Docs
	CurrentDoc docInfo      // the single record displayed on this page
	DocsJSON   template.JS  // JSON-encoded Docs for in-batch JS navigation
}

var (
	cfg       Config
	fsClient  *firestore.Client
	templates *template.Template
)

func main() {
	// Determine config file path (allow override via env).
	configPath := os.Getenv("CONFIG_FILE")
	if configPath == "" {
		configPath = "config.yaml"
	}

	if err := loadConfig(configPath); err != nil {
		log.Fatalf("failed to load config from %s: %v", configPath, err)
	}

	// Load embedded templates from the templates/ directory next to the binary.
	execDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatalf("failed to determine executable directory: %v", err)
	}
	tmplPattern := filepath.Join(execDir, "templates", "*.html")
	templates, err = template.New("").ParseGlob(tmplPattern)
	if err != nil {
		// Fallback: try relative path (useful when running `go run .`)
		templates, err = template.New("").ParseGlob("templates/*.html")
		if err != nil {
			log.Fatalf("failed to parse templates: %v", err)
		}
	}

	// Build Firestore client options.
	var clientOpts []option.ClientOption
	if cfg.CredentialsFile != "" {
		clientOpts = append(clientOpts, option.WithCredentialsFile(cfg.CredentialsFile))
	}

	ctx := context.Background()
	fsClient, err = firestore.NewClient(ctx, cfg.ProjectID, clientOpts...)
	if err != nil {
		log.Fatalf("failed to create Firestore client: %v", err)
	}
	defer fsClient.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/collection/", collectionHandler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("FireScan listening on %s (project: %s)", addr, cfg.ProjectID)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// loadConfig reads and parses the YAML configuration file.
func loadConfig(path string) error {
	cfg = Config{} // reset to zero value before parsing
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing config file: %w", err)
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 25
	}
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}
	return nil
}

// indexHandler renders the index page with collection names and document counts.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	data := indexData{ProjectID: cfg.ProjectID}

	for _, name := range cfg.Collections {
		count, err := countDocuments(ctx, name)
		if err != nil {
			log.Printf("error counting %s: %v", name, err)
			count = -1
		}
		data.Collections = append(data.Collections, collectionInfo{Name: name, Count: count})
	}

	renderTemplate(w, "index.html", data)
}

// collectionHandler renders a single-record view of a Firestore collection.
// It preloads a full batch into memory so the client can navigate within the
// batch instantly; a new network request is only made when paging past the batch.
func collectionHandler(w http.ResponseWriter, r *http.Request) {
	// Extract collection name from path: /collection/<name>
	name := strings.TrimPrefix(r.URL.Path, "/collection/")
	name = strings.Trim(name, "/")
	if name == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// "page" in the URL represents the 1-based record number to display.
	record := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			record = n
		}
	}

	ctx := r.Context()

	// Count total documents for HasPrev / HasNext and the record counter.
	total, err := countDocuments(ctx, name)
	if err != nil {
		log.Printf("error counting %s: %v", name, err)
		total = 0
	}

	// Determine which batch contains this record and fetch it.
	// batchOffset is the 0-based collection offset of the first doc in the batch.
	batchOffset := ((record - 1) / cfg.BatchSize) * cfg.BatchSize
	docs, err := fetchDocuments(ctx, name, batchOffset, cfg.BatchSize)
	if err != nil {
		http.Error(w, fmt.Sprintf("error fetching documents: %v", err), http.StatusInternalServerError)
		return
	}
	if docs == nil {
		docs = []docInfo{}
	}

	// Pick the doc that corresponds to the requested record number.
	indexInBatch := (record - 1) - batchOffset // 0-based index within docs
	var currentDoc docInfo
	if indexInBatch >= 0 && indexInBatch < len(docs) {
		currentDoc = docs[indexInBatch]
	}

	// Encode the entire batch as JSON for in-browser navigation.
	docsJSON, err := json.Marshal(docs)
	if err != nil {
		docsJSON = []byte("[]")
	}

	data := collectionData{
		Collection: name,
		Page:       record,
		TotalPages: total,
		Total:      total,
		HasPrev:    record > 1,
		HasNext:    record < total,
		Docs:       docs,
		BatchStart: batchOffset + 1, // 1-based record number of the first doc in Docs
		CurrentDoc: currentDoc,
		DocsJSON:   template.JS(docsJSON),
	}

	renderTemplate(w, "collection.html", data)
}

// countDocuments returns the number of documents in a Firestore collection.
func countDocuments(ctx context.Context, collection string) (int, error) {
	results, err := fsClient.Collection(collection).NewAggregationQuery().WithCount("count").Get(ctx)
	if err != nil {
		return 0, err
	}
	countVal, ok := results["count"]
	if !ok {
		return 0, fmt.Errorf("count field missing from aggregation result")
	}
	pbVal, ok := countVal.(*firestorepb.Value)
	if !ok {
		return 0, fmt.Errorf("unexpected type for count: %T", countVal)
	}
	return int(pbVal.GetIntegerValue()), nil
}

// fetchDocuments retrieves up to limit documents from a collection starting at offset,
// ordered by timestamp descending.
func fetchDocuments(ctx context.Context, collection string, offset, limit int) ([]docInfo, error) {
	q := fsClient.Collection(collection).
		OrderBy("timestamp", firestore.Desc).
		Offset(offset).
		Limit(limit)

	iter := q.Documents(ctx)
	defer iter.Stop()

	var docs []docInfo
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		raw := snap.Data()
		prettyJSON, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			prettyJSON = []byte(fmt.Sprintf("<error: %v>", err))
		}

		ts := ""
		if t, ok := raw["timestamp"]; ok {
			switch v := t.(type) {
			case time.Time:
				ts = v.UTC().Format(time.RFC3339)
			case *firestore.DocumentRef:
				// ignore
			}
		}

		docs = append(docs, docInfo{
			ID:        snap.Ref.ID,
			JSON:      string(prettyJSON),
			Timestamp: ts,
		})
	}
	return docs, nil
}

// renderTemplate executes a named template, writing the result to w.
func renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template error (%s): %v", name, err)
		http.Error(w, "internal template error", http.StatusInternalServerError)
	}
}
