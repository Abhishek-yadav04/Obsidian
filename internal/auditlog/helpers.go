// Copyright 2024 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

package auditlog

import (
	"fmt"
	"strings"

	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
)

func getRequestHeaders(al plugintypes.AuditLog) map[string]string {
	reqHeaders := map[string]string{}
	if al.Transaction().Request() == nil {
		return reqHeaders
	}
	for k, v := range al.Transaction().Request().Headers() {
		reqHeaders[k] = strings.Join(v, ", ")
	}
	return reqHeaders
}

func getResponseHeaders(al plugintypes.AuditLog) map[string]string {
	resHeaders := map[string]string{}
	if al.Transaction().Response() == nil {
		return resHeaders
	}
	for k, v := range al.Transaction().Response().Headers() {
		resHeaders[k] = strings.Join(v, ", ")
	}
	return resHeaders
}

func getRequestLine(al plugintypes.AuditLog) string {
	if al.Transaction().Request() == nil {
		return ""
	}
	return fmt.Sprintf(
		"%s %s %s",
		al.Transaction().Request().Method(),
		al.Transaction().Request().URI(),
		al.Transaction().Request().HTTPVersion(),
	)
}
