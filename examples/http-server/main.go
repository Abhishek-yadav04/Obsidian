package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/corazawaf/coraza/v3"
	txhttp "github.com/corazawaf/coraza/v3/http"
	"github.com/corazawaf/coraza/v3/types"
	"github.com/gorilla/websocket"
	"github.com/jung-kurt/gofpdf"
)

// =====================
// Attack Log Structure
// =====================
type AttackLog struct {
	Time     string `json:"time"`
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Type     string `json:"type"` // SQLI / BODY / OTHER
}

var (
	attackLogs []AttackLog
	logMutex   sync.Mutex
	upgrader   = websocket.Upgrader{}
	clients    = make(map[*websocket.Conn]bool)
)

// =====================
// Example Protected App
// =====================
func exampleHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	resBody := "Hello world, transaction not disrupted."

	if body := os.Getenv("RESPONSE_BODY"); body != "" {
		resBody = body
	}

	if h := os.Getenv("RESPONSE_HEADERS"); h != "" {
		key, val, _ := strings.Cut(h, ":")
		w.Header().Set(key, val)
	}

	w.Write([]byte(resBody))
}

// =====================
// Main Function
// =====================
func main() {
	waf := createWAF()

	// Protected example app
	http.Handle("/", txhttp.WrapHandler(waf, http.HandlerFunc(exampleHandler)))

	// API to fetch logs
	http.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		logMutex.Lock()
		defer logMutex.Unlock()
		if attackLogs == nil {
			json.NewEncoder(w).Encode([]AttackLog{})
			return
		}
		json.NewEncoder(w).Encode(attackLogs)
	})

	// API to fetch stats for charts
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sqli, body := 0, 0
		logMutex.Lock()
		for _, l := range attackLogs {
			if l.Type == "SQLI" {
				sqli++
			} else if l.Type == "BODY" {
				body++
			}
		}
		logMutex.Unlock()
		json.NewEncoder(w).Encode(map[string]int{"sqli": sqli, "body": body})
	})

	// PDF export
	http.HandleFunc("/export", func(w http.ResponseWriter, r *http.Request) {
		pdf := gofpdf.New("P", "mm", "A4", "")
		pdf.AddPage()
		pdf.SetFont("Arial", "B", 16)
		pdf.Cell(40, 10, "Obsidian WAF Attack Report")
		pdf.Ln(12)
		pdf.SetFont("Arial", "", 12)
		logMutex.Lock()
		for _, l := range attackLogs {
			pdf.Cell(0, 8, fmt.Sprintf("[%s] %s | %s | %s", l.Time, l.RuleID, l.Severity, l.Message))
			pdf.Ln(8)
		}
		logMutex.Unlock()
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename=report.pdf")
		err := pdf.Output(w)
		if err != nil {
			fmt.Println("PDF error:", err)
		}
	})

	// WebSocket for live updates
	http.HandleFunc("/ws", wsHandler)

	// Serve UI with login protection
	http.Handle("/ui/", http.StripPrefix("/ui/", authMiddleware(http.FileServer(http.Dir("./ui")))))

	// Serve login page without auth
	http.HandleFunc("/ui/login.html", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./ui/login.html")
	})

	fmt.Println("Server running on http://localhost:8090")
	fmt.Println("Dashboard available at http://localhost:8090/ui/")

	log.Fatal(http.ListenAndServe(":8090", nil))
}

// =====================
// Create WAF
// =====================
func createWAF() coraza.WAF {
	directivesFile := "./default.conf"
	if s := os.Getenv("DIRECTIVES_FILE"); s != "" {
		directivesFile = s
	}

	waf, err := coraza.NewWAF(
		coraza.NewWAFConfig().
			WithErrorCallback(logError).
			WithDirectivesFromFile(directivesFile),
	)
	if err != nil {
		log.Fatal(err)
	}
	return waf
}

// =====================
// WAF Error Callback
// =====================
func logError(err types.MatchedRule) {
	msg := err.ErrorLog()

	typ := "OTHER"
	if strings.Contains(strings.ToLower(msg), "password") {
		typ = "BODY"
	} else if strings.Contains(strings.ToLower(msg), "select") || strings.Contains(strings.ToLower(msg), "union") {
		typ = "SQLI"
	}

	logEntry := AttackLog{
		Time:     time.Now().Format(time.RFC3339),
		RuleID:   fmt.Sprintf("%d", err.Rule().ID()),
		Severity: err.Rule().Severity().String(),
		Message:  msg,
		Type:     typ,
	}

	logMutex.Lock()
	attackLogs = append(attackLogs, logEntry)
	logMutex.Unlock()

	// Broadcast to WebSocket clients
	broadcastWS(logEntry)

	fmt.Printf("[WAF][%s] %s\n", logEntry.Severity, logEntry.Message)
}

// =====================
// WebSocket Handler
// =====================
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("WebSocket upgrade error:", err)
		return
	}
	defer conn.Close()
	clients[conn] = true
	for {
		if _, _, err := conn.NextReader(); err != nil {
			delete(clients, conn)
			break
		}
	}
}

func broadcastWS(entry AttackLog) {
	logMutex.Lock()
	defer logMutex.Unlock()
	for c := range clients {
		c.WriteJSON(entry)
	}
}

// =====================
// Auth Middleware
// =====================
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow login page without cookie
		if strings.HasSuffix(r.URL.Path, "login.html") {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie("auth")
		if err != nil || cookie.Value != "admin" {
			http.Redirect(w, r, "/ui/login.html", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}
