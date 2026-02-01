// Copyright 2022 Juan Pablo Tosso and the OWASP Coraza contributors
// SPDX-License-Identifier: Apache-2.0

//go:build coraza.rule.multiphase_evaluation

package corazawaf

import (
	"testing"

	"github.com/corazawaf/coraza/v3/types/variables"
)

const errExceptionsFmt = "got %d exceptions, expected %d"

func assertException(t *testing.T, vars []variable, idx, want int, key string) {
	t.Helper()
	if len(vars[idx].Exceptions) != want {
		t.Errorf(errExceptionsFmt, len(vars[idx].Exceptions), want)
		return
	}
	if key != "" && vars[idx].Exceptions[want-1].KeyStr != key {
		t.Errorf("expected exception key %s, got %s", key, vars[idx].Exceptions[want-1].KeyStr)
	}
}

func TestARGSSplit(t *testing.T) {
	rule := NewRule()
	key := "something"
	if err := rule.AddVariable(variables.Args, key, false); err != nil {
		t.Error(err)
	}
	if len(rule.variables) != 2 {
		t.Fatalf("Expected 2 variables, got %d", len(rule.variables))
	}
	if rule.variables[0].Variable != variables.ArgsGet &&
		rule.variables[1].Variable != variables.ArgsPost {
		t.Errorf("Expected variables ArgsGet and ArgsPost")
	}
	if rule.variables[0].KeyStr != key && rule.variables[1].KeyStr != key {
		t.Errorf("Expected keys equal to %s, got: %s and %s", key, rule.variables[0].KeyStr, rule.variables[1].KeyStr)
	}
}

func TestARGSNamesSplit(t *testing.T) {
	rule := NewRule()
	key := "name"
	if err := rule.AddVariable(variables.ArgsNames, key, false); err != nil {
		t.Error(err)
	}
	if len(rule.variables) != 2 {
		t.Fatalf("Expected 2 variables, got %d", len(rule.variables))
	}
	if rule.variables[0].Variable != variables.ArgsGetNames &&
		rule.variables[1].Variable != variables.ArgsPostNames {
		t.Errorf("Expected ArgsGetNames and ArgsPostNames variables")
	}
	if rule.variables[0].KeyStr != key && rule.variables[1].KeyStr != key {
		t.Errorf("Expected keys equal to %s, got: %s and %s", key, rule.variables[0].KeyStr, rule.variables[1].KeyStr)
	}
}

func TestRuleNegativeVariablesMulti(t *testing.T) {
	rule := NewRule()
	if err := rule.AddVariable(variables.Args, "", false); err != nil {
		t.Error(err)
	}
	// [0] ArgsGet
	// [1] ArgsPost
	if rule.variables[0].Variable != variables.ArgsGet && rule.variables[1].Variable != variables.ArgsPost {
		t.Error("Variable ARGS has not been properly added and splitted into ArgsPost ArgsGet")
	}
	if rule.variables[0].KeyRx != nil && rule.variables[1].KeyRx != nil {
		t.Error("invalid key type for variables")
	}

	if err := rule.AddVariableNegation(variables.Args, "test"); err != nil {
		t.Error(err)
	}

	assertException(t, rule.variables, 0, 1, "test")
	assertException(t, rule.variables, 1, 1, "test")

	if err := rule.AddVariable(variables.Args, "/test.*/", false); err != nil {
		t.Error(err)
	}
	// [0] ArgsGet name (1 exception)
	// [1] ArgsPost name (1 exception)
	// [2] ArgsGet regex
	// [3] ArgsPost regex
	if rule.variables[2].KeyRx == nil || rule.variables[2].KeyRx.String() != "test.*" {
		t.Error("variable regex cannot be nil")
	}
	if rule.variables[3].KeyRx == nil || rule.variables[3].KeyRx.String() != "test.*" {
		t.Error("variable regex cannot be nil")
	}

	// [0] ArgsGet name (2 exceptions)
	// [1] ArgsPost name (2 exceptions)
	// [2] ArgsGet regex (1 exception)
	// [3] ArgsPost regex (1 exception)
	if err := rule.AddVariableNegation(variables.Args, "test2"); err != nil {
		t.Error(err)
	}

	assertException(t, rule.variables, 0, 2, "test2")
	assertException(t, rule.variables, 1, 2, "test2")
	assertException(t, rule.variables, 2, 1, "")
	assertException(t, rule.variables, 3, 1, "")

}
