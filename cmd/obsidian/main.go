package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
	"github.com/corazawaf/coraza/v3/internal/app/api"
	"github.com/corazawaf/coraza/v3/internal/app/store"
	"github.com/corazawaf/coraza/v3/internal/app/waf"
	"github.com/corazawaf/coraza/v3/types"
)

//go:embed ui/*
var uiAssets embed.FS

func main() {
	port := flag.Int("port", 8082, "Port to run the server on")
	dev := flag.Bool("dev", false, "Run in dev mode")
	flag.Parse()

	// Initialize Store
	s := store.NewStore("data.json")
	if *dev {
		fmt.Println("Running in Dev Mode")
	}

	// Initialize WAF
	wafEngine, err := waf.NewWAF(s)
	if err != nil {
		log.Fatal(err)
	}

	// Initialize API
	apiHandler := api.NewAPI(s)

	// Router
	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/login", apiHandler.HandleLogin)
	mux.HandleFunc("/api/stats", apiHandler.HandleStats)
	mux.HandleFunc("/api/logs", apiHandler.HandleLogs)
	mux.HandleFunc("/api/rules", apiHandler.HandleRules)
	mux.HandleFunc("/api/ws", apiHandler.HandleWS)
	mux.HandleFunc("/api/export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4... (Mock PDF)"))
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Static Files (UI)
	// We need to strip "ui" prefix because embed root is "ui"
	uiFS, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(uiFS))
	// We handle root / by stripping nothing if we just passed uiFS, but we want / to match index.html
	// FileServer handles index.html mostly automatically.
	mux.Handle("/", fileServer)

	// Middleware Stack
	finalHandler := loggingMiddleware(wafMiddleware(wafEngine, s, mux))

	fmt.Printf("Obsidian Sentinel WAF is running on http://localhost:%d\n", *port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), finalHandler); err != nil {
		log.Fatal(err)
	}
}

func wafMiddleware(engine types.WAF, s *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip WAF for static assets (css, js, images) to save perf
		// Simple check for extension
		path := r.URL.Path
		if path == "/api/ws" { // Skip WAF for websockets for now
			next.ServeHTTP(w, r)
			return
		}
		
		tx := engine.NewTransaction()
		// Capture ID for logging
		// txID := tx.ID()
		
		// Ensure cleanup
		defer func() {
			tx.ProcessLogging()
			tx.Close()
		}()

		// 1. Process Request Headers
		tx.ProcessConnection(r.RemoteAddr, 0, "", 0)
		tx.ProcessURI(r.URL.String(), r.Method, r.Proto)
		for k, vr := range r.Header {
			for _, v := range vr {
				tx.AddRequestHeader(k, v)
			}
		}
		if it := tx.ProcessRequestHeaders(); it != nil {
			processInterruption(w, it, s, tx)
			return
		}

		// 2. Process Request Body (Simplified)
		// For a real WAF we need to buffer body, write to tx, then new reader for next handler
		// Skipping heavy body buffering for this demo for simplicity unless needed
		if it := tx.ProcessRequestBody(); it != nil {
			processInterruption(w, it, s, tx)
			return
		}

		// 3. Pass to application
		// We intercept Response to run Response Rules (Phase 4)
		rec := &responseRecorder{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rec, r)

		// 4. Process Response
		for k, vr := range rec.Header() {
			for _, v := range vr {
				tx.AddResponseHeader(k, v)
			}
		}
		if it := tx.ProcessResponseHeaders(rec.statusCode, "HTTP/1.1"); it != nil {
			// Too late to intercept status code usually if already written, but we can try
			// In standard Go http, once you write, it's gone.
			// So we mainly log here for alerting.
		}

		// Update Safe Request Count if no interruption
		if !tx.IsInterrupted() {
			s.IncrementSafeRequest()
		}
	})
}

func processInterruption(w http.ResponseWriter, it *types.Interruption, s *store.Store, tx types.Transaction) {
	w.WriteHeader(403)
	w.Write([]byte("WAF Blocked: " + it.Action))
	
	// Ensure log is written (store uses a workaround via callback but let's be safe)
	// Actually the audit logger hybrid handles this via ProcessLogging() in defer.
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Access Log
		fmt.Printf("[%s] %s %s %v\n", time.Now().Format(time.RFC3339), r.Method, r.URL.Path, time.Since(start))
	})
}

// Helper for response interception (simplified)
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *responseRecorder) WriteHeader(code int) {
	rec.statusCode = code
	rec.ResponseWriter.WriteHeader(code)
}
