// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

package corazawaf

import (
	"github.com/corazawaf/coraza/v3/experimental/plugins/macro"
	"github.com/corazawaf/coraza/v3/types"
)

func newTestRule(id int) *Rule {
	r := NewRule()
	r.ID_ = id
	r.Phase_ = types.PhaseRequestHeaders
	r.Msg, _ = macro.NewMacro("test")
	return r
}
