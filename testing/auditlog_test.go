// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

// Audit logs are currently disabled for tinygo

//go:build !tinygo

package testing

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/corazawaf/coraza/v3/internal/auditlog"
	"github.com/corazawaf/coraza/v3/internal/corazawaf"
	"github.com/corazawaf/coraza/v3/internal/seclang"
)

const (
	secAuditLogFormat     = "SecAuditLog %s"
	errExpectedMsgCount   = "Expected 1 message, got %d"
	msgUnconditionalMatch = "unconditional match"
)

type auditTest struct {
	t      *testing.T
	waf    *corazawaf.WAF
	parser *seclang.Parser
	log    string
}

func (at *auditTest) newTransaction() *corazawaf.Transaction {
	tx := at.waf.NewTransaction()
	// On Windows, the logger keeps the file locked. We must switch the log to release the lock.
	at.t.Cleanup(func() {
		at.parser.FromString(fmt.Sprintf(secAuditLogFormat, os.DevNull))
		at.waf.AuditLogWriter().Close()
	})
	return tx
}

func (at *auditTest) openLog() *os.File {
	file, err := os.Open(at.log)
	if err != nil {
		at.t.Fatal(err)
	}
	at.t.Cleanup(func() { file.Close() })
	return file
}

func setupAuditTest(t *testing.T, rules string) *auditTest {
	t.Helper()
	at := &auditTest{
		t:   t,
		waf: corazawaf.NewWAF(),
	}
	at.parser = seclang.NewParser(at.waf)
	at.log = newTempLogFile(t)
	if err := at.parser.FromString(fmt.Sprintf(secAuditLogFormat, at.log)); err != nil {
		t.Fatal(err)
	}
	if err := at.parser.FromString(rules); err != nil {
		t.Fatal(err)
	}
	return at
}

func newTempLogFile(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp("", "auditlog-*.log")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
	return path
}

func TestAuditLogMessages(t *testing.T) {
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts ABCDEFGHIJKZ
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,msg:'unconditional match'"
	`)
	tx := at.newTransaction()
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
	file := at.openLog()
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
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine RelevantOnly
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogRelevantStatus 401
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,msg:'unconditional match'"
	`)
	tx := at.newTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	// We re-open the file to ensure we can read what WAF wrote (and handle Windows locking)
	file := at.openLog()
	var al2 auditlog.Log
	// this should fail, there should be no log
	if err := json.NewDecoder(file).Decode(&al2); err == nil {
		t.Error(err)
	}
}

func TestAuditLogRelevantOnlyOk(t *testing.T) {
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine RelevantOnly
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogRelevantStatus ".*"
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,msg:'unconditional match'"
	`)
	tx := at.newTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	file := at.openLog()
	var al2 auditlog.Log
	// this should pass as it matches any status
	if err := json.NewDecoder(file).Decode(&al2); err != nil {
		t.Error(err)
	}
}

func TestAuditLogRelevantOnlyNoAuditlog(t *testing.T) {
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine RelevantOnly
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogRelevantStatus ".*"
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,noauditlog,msg:'unconditional match'"
	`)
	tx := at.newTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	file := at.openLog()
	var al2 auditlog.Log
	// there should be no audit log because of noauditlog
	if err := json.NewDecoder(file).Decode(&al2); err == nil {
		t.Errorf("there should be no audit log, got %v", al2)
	}
}

func TestAuditLogOnWithNoLog(t *testing.T) {
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts ABCHIJKZ
		SecAuditLogRelevantStatus ".*"
		# auditlog tells that the transaction will have to log matches meant to be logged (not the ones with nolog)
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,nolog,msg:'nolog message'"
	`)
	tx := at.newTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	file := at.openLog()
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
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
	`)
	tx := at.newTransaction()
	uri := "/some-url"
	method := "POST"
	proto := "HTTP/1.1"
	tx.ProcessURI(uri, method, proto)
	tx.ProcessLogging()
	// now we read file
	file := at.openLog()
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
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecRequestBodyAccess On
	`)
	tx := at.newTransaction()
	params := "somepost=data"
	var err error
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
	file := at.openLog()
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
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts AHZ
		SecAuditLogRelevantStatus ".*"
		# An audit log should contain messages section on H flag included
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,auditlog,msg:'expected rule message'"
	`)
	tx := at.newTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	file := at.openLog()
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
	at := setupAuditTest(t, `
		SecRuleEngine DetectionOnly
		SecAuditEngine On
		SecAuditLogFormat json
		SecAuditLogType serial
		SecAuditLogParts ABCKZ
		SecAuditLogRelevantStatus ".*"
		# auditlog should not contain error logs without H flag included
		SecRule ARGS "@unconditionalMatch" "id:1,phase:1,log,auditlog,msg:'unexpected logged message'"
	`)
	tx := at.newTransaction()
	tx.AddGetRequestArgument("test", "test")
	tx.ProcessRequestHeaders()
	tx.ProcessLogging()
	// now we read file
	file := at.openLog()
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
