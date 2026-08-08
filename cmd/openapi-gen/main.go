package main

import (
	"fmt"
	"os"

	"batchscope/internal/app"
)

func main() {
	specYAML, err := app.OpenAPISpec().YAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate OpenAPI YAML: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stdout.Write(specYAML); err != nil {
		fmt.Fprintf(os.Stderr, "write OpenAPI YAML: %v\n", err)
		os.Exit(1)
	}
}
