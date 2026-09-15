package api

import _ "embed"

// OpenAPI is the canonical REST contract shared by the Go and TypeScript implementations.
//
//go:embed openapi.yaml
var OpenAPI []byte
