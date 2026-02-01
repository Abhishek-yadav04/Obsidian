// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

// Audit logs are currently disabled for tinygo

//go:build !tinygo

package testing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corazawaf/coraza/v3/internal/auditlog"
	"github.com/corazawaf/coraza/v3/internal/corazawaf"
	"github.com/corazawaf/coraza/v3/internal/seclang"
)

const (
	tmpLogFilename        = "tmp.log"
	secAuditLogFormat     = "SecAuditLog %s"
	errExpectedMsgCount   = "Expected 1 message, got %d"
	msgUnconditionalMatch = "unconditional match"
)

func TestAuditLogMessages(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), tmpLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	logPath := file.Name()
	file.Close() // Close to avoid locking issues on Windows
	if err := parser.FromString(fmt.Sprintf(secAuditLogFormat, logPath)); err != nil {
		t.Fatal(err)
	}
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts ABCDEFGHIJKZ
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,msg:'unconditional match'"
	`); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	// On Windows, the logger keeps the file locked. We must switch the log to release the lock.
	defer parser.FromString(fmt.Sprintf(secAuditLogFormat, os.DevNull))

	tx := waf.NewTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	al := tx.AuditLog()
	if len(al.Messages()) != 1 {
		t.Errorf(errExpectedMsgCount, len(al.Messages()))
	}
	if al.Messages()[0].Message() != msgUnconditionalMatch {
		t.Errorf("Expected message %q, got %q", msgUnconditionalMatch, al.Messages()[0].Message())
	}
	tx.ProcessLogging()
	// now we read file
	file, err = os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var al2 auditlog.Log
	if err := json.NewDecoder(file).Decode(&al2); err != nil {
		t.Error(err)
	}
	if len(al2.Messages()) != 1 {
		t.Fatalf(errExpectedMsgCount, len(al2.Messages()))
	}
	if al2.Messages()[0].Message() != msgUnconditionalMatch {
		t.Errorf("Expected message %q, got %q", msgUnconditionalMatch, al2.Messages()[0].Message())
	}
}

func TestAuditLogRelevantOnly(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine RelevantOnly
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogRelevantStatus 401
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,msg:'unconditional match'"
	`); err != nil {
		t.Fatal(err)
	}
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), tmpLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	logPath := file.Name()
	file.Close()
	defer os.Remove(logPath)
	if err := parser.FromString(fmt.Sprintf(secAuditLogFormat, logPath)); err != nil {
		t.Fatal(err)
	}
	tx := waf.NewTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	// We re-open the file to ensure we can read what WAF wrote (and handle Windows locking)
	file, err = os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var al2 auditlog.Log
	// this should fail, there should be no log
	if err := json.NewDecoder(file).Decode(&al2); err == nil {
		t.Error(err)
	}
}

func TestAuditLogRelevantOnlyOk(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), tmpLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if err := parser.FromString(fmt.Sprintf(secAuditLogFormat, file.Name())); err != nil {
		t.Fatal(err)
	}
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine RelevantOnly
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogRelevantStatus ".*"
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,msg:'unconditional match'"
	`); err != nil {
		t.Fatal(err)
	}

	tx := waf.NewTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	if _, err := file.Seek(0, 0); err != nil {
		t.Error(err)
	}
	var al2 auditlog.Log
	// this should pass as it matches any status
	if err := json.NewDecoder(file).Decode(&al2); err != nil {
		t.Error(err)
	}
}

func TestAuditLogRelevantOnlyNoAuditlog(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine RelevantOnly
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogRelevantStatus ".*"
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,noauditlog,msg:'unconditional match'"
	`); err != nil {
		t.Fatal(err)
	}
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), tmpLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if err := parser.FromString(fmt.Sprintf(secAuditLogFormat, file.Name())); err != nil {
		t.Fatal(err)
	}
	tx := waf.NewTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	if _, err := file.Seek(0, 0); err != nil {
		t.Error(err)
	}
	var al2 auditlog.Log
	// there should be no audit log because of noauditlog
	if err := json.NewDecoder(file).Decode(&al2); err == nil {
		t.Errorf("there should be no audit log, got %v", al2)
	}
}

func TestAuditLogOnWithNoLog(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts ABCHIJKZ
		SecAuditLogRelevantStatus ".*"
		# auditlog tells that the transaction will have to log matches meant to be logged (not the ones with nolog)
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,nolog,msg:'nolog message'"
	`); err != nil {
		t.Fatal(err)
	}
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), tmpLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if err := parser.FromString(fmt.Sprintf(secAuditLogFormat, file.Name())); err != nil {
		t.Fatal(err)
	}
	tx := waf.NewTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	if _, err := file.Seek(0, 0); err != nil {
		t.Error(err)
	}
	var al2 auditlog.Log
	// there should be no audit log because of nolog
	if err := json.NewDecoder(file).Decode(&al2); err == nil {
		if al2.Messages() != nil {
			t.Errorf("Unexpected rule logged")
		}
	} else {
		t.Error(err)
	}
}

func TestAuditLogRequestMethodURIProtocol(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
	`); err != nil {
		t.Fatal(err)
	}
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), tmpLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if err := parser.FromString(fmt.Sprintf(secAuditLogFormat, file.Name())); err != nil {
		t.Fatal(err)
	}
	tx := waf.NewTransaction()

	uri := "/some-url"
	method := "POST"
	proto := "HTTP/1.1"

	tx.ProcessURI(uri, method, proto)
	tx.ProcessLogging()
	// now we read file
	if _, err := file.Seek(0, 0); err != nil {
		t.Error(err)
	}
	var al2 auditlog.Log
	if err := json.NewDecoder(file).Decode(&al2); err != nil {
		t.Error(err)
	}
	trans := al2.Transaction()
	if trans == nil {
		t.Fatalf("Expected 1 transaction, got nil")
	}
	req := trans.Request()
	if req == nil {
		t.Fatalf("Expected 1 request, got nil")
	}
	if req.URI() != uri {
		t.Fatalf("Expected %s uri, got %s", uri, req.URI())
	}
	if req.Method() != method {
		t.Fatalf("Expected %s method, got %s", method, req.Method())
	}
	if req.Protocol() != proto {
		t.Fatalf("Expected %s protocol, got %s", proto, req.Protocol())
	}
}

func TestAuditLogRequestBody(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecRequestBodyAccess On
	`); err != nil {
		t.Fatal(err)
	}
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), "tmp.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if err := parser.FromString(fmt.Sprintf("SecAuditLog %s", file.Name())); err != nil {
		t.Fatal(err)
	}
	tx := waf.NewTransaction()
	params := "somepost=data"
	_, _, err = tx.ReadRequestBodyFrom(strings.NewReader(params))
	if err != nil {
		t.Error(err)
	}
	_, err = tx.ProcessRequestBody()
	if err != nil {
		t.Error(err)
	}
	tx.ProcessLogging()
	// now we read file
	if _, err := file.Seek(0, 0); err != nil {
		t.Error(err)
	}
	var al2 auditlog.Log
	if err := json.NewDecoder(file).Decode(&al2); err != nil {
		t.Error(err)
	}
	trans := al2.Transaction()
	if trans == nil {
		t.Fatalf("Expected 1 transaction, got nil")
	}
	req := trans.Request()
	if req == nil {
		t.Fatalf("Expected 1 request, got nil")
	}
	if req.Body() != params {
		t.Fatalf("Expected %s uri, got %s", params, req.Body())
	}
}

// Arule expected to be logged (log and auditlog flags enabled) should
// print the error message in the audit log as part of the H section.
func TestAuditLogHFlag(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts AHZ
		SecAuditLogRelevantStatus ".*"
		# An audit log should contain messages section on H flag included
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,auditlog,msg:'expected rule message'"
	`); err != nil {
		t.Fatal(err)
	}
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), "tmp.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if err := parser.FromString(fmt.Sprintf("SecAuditLog %s", file.Name())); err != nil {
		t.Fatal(err)
	}
	tx := waf.NewTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	if _, err := file.Seek(0, 0); err != nil {
		t.Error(err)
	}
	var al auditlog.Log
	if err := json.NewDecoder(file).Decode(&al); err != nil {
		t.Error(err)
	}
	if len(al.Messages()) != 1 {
		t.Fatalf(errExpectedMsgCount, len(al.Messages()))
	}
	type auditLogWithErrMesg interface{ ErrorMessage() string }
	alWithErrMsg, ok := al.Messages()[0].(auditLogWithErrMesg)
	if !ok {
		t.Fatalf("Expected message to be of type auditLogWithErrMesg")
	}
	expected := "expected rule message"
	if !strings.Contains(alWithErrMsg.ErrorMessage(), expected) {
		t.Errorf("Expected audit log to contain %q, got %q", expected, alWithErrMsg.ErrorMessage())
	}
}

func TestAuditLogWithKFlagWithoutHFlag(t *testing.T) {
	waf := corazawaf.NewWAF()
	parser := seclang.NewParser(waf)
	if err := parser.FromString(`
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts ABCKZ
		SecAuditLogRelevantStatus ".*"
		# auditlog should not contain error logs without H flag included
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,auditlog,msg:'unexpected logged message'"
	`); err != nil {
		t.Fatal(err)
	}
	// generate a random tmp file
	file, err := os.Create(filepath.Join(t.TempDir(), tmpLogFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	if err := parser.FromString(fmt.Sprintf(secAuditLogFormat, file.Name())); err != nil {
		t.Fatal(err)
	}
	tx := waf.NewTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	if _, err := file.Seek(0, 0); err != nil {
		t.Error(err)
	}
	var al auditlog.Log
	if err := json.NewDecoder(file).Decode(&al); err != nil {
		t.Error(err)
	}
	if len(al.Messages()) != 1 {
		t.Fatalf(errExpectedMsgCount, len(al.Messages()))
	}
	type auditLogWithErrMesg interface{ ErrorMessage() string }
	alWithErrMsg, ok := al.Messages()[0].(auditLogWithErrMesg)
	if !ok {
		t.Fatalf("Expected message to be of type auditLogWithErrMesg")
	}
	notExpected := "unexpected logged message"
	if strings.Contains(alWithErrMsg.ErrorMessage(), notExpected) {
		t.Errorf("Not expected audit log to contain %q, got %q", notExpected, alWithErrMsg.ErrorMessage())
	}
}
