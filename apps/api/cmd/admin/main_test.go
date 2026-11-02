package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsBeforeConnecting(t *testing.T) {
	noEnv := func(string) string { return "" }
	cases := map[string][]string{
		"no command":      nil,
		"unknown command": {"delete-tenant"},
		"unknown flag":    {"create-tenant", "-nme", "x"},
		"extra argument":  {"create-tenant", "-name", "x", "-jurisdiction", "DE", "-timezone", "Europe/Berlin", "oops"},
		"invalid input":   {"create-tenant", "-name", "x", "-jurisdiction", "JP", "-timezone", "Asia/Tokyo"},
		"no database url": {"create-tenant", "-name", "x", "-jurisdiction", "DE", "-timezone", "Europe/Berlin"},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := run(t.Context(), args, &stdout, &stderr, noEnv); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", name, code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: wrote to stdout: %q", name, stdout.String())
		}
	}
}

func TestParseCreateTenant(t *testing.T) {
	var stderr bytes.Buffer
	in, err := parseCreateTenant(strings.Fields("-name Test -jurisdiction JP -timezone Asia/Tokyo -jp-tax-rounding down -fiscal-year-end-month 3 -key-expires-in 720h"), &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "Test" || in.Jurisdiction != "JP" || in.JPTaxRounding != "down" || in.FiscalYearEndMonth != 3 || in.KeyExpiresAt == nil || in.KeyName != "initial" {
		t.Errorf("parsed %+v", in)
	}
}
