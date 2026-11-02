// Package api holds the Go types and the strict net/http server interface
// generated from openapi/openapi.yaml, and a copy of the contract itself for
// run-time request validation. Do not edit api.gen.go or openapi.yaml here by
// hand; change the contract and run `go generate ./...` instead.
package api

import _ "embed"

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../../openapi/openapi.yaml
//go:generate go run ./copyspec ../../../../openapi/openapi.yaml openapi.yaml

// Spec is openapi/openapi.yaml, embedded so the deployed binary can validate
// requests without reading files outside apps/api.
//
//go:embed openapi.yaml
var Spec []byte
