// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

package seclang

import (
	"testing"

	"github.com/corazawaf/coraza/v3/internal/corazawaf"
)

func setup(t *testing.T) (*corazawaf.WAF, *Parser) {
	t.Helper()
	waf := corazawaf.NewWAF()
	return waf, NewParser(waf)
}
