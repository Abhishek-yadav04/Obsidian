// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

package transformations

import (
	"strings"
)

func upperCase(data string) (string, bool, error) {
	// An explicit ToUpper implementation could avoid the byte-by-byte comparison for the changed boolean.
	// See https://github.com/corazawaf/coraza/pull/778#discussion_r1186963422
	transformedData := strings.ToUpper(data)
	return transformedData, data != transformedData, nil
}
