// Command openapi-lint validates the Record Hub OpenAPI document and its
// project-specific contract rules.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: openapi-lint <openapi-file>")
		os.Exit(2)
	}

	document, err := openapi3.NewLoader().LoadFromFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load OpenAPI document: %v\n", err)
		os.Exit(1)
	}
	if err := document.Validate(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "validate OpenAPI document: %v\n", err)
		os.Exit(1)
	}
	if err := lint(document); err != nil {
		fmt.Fprintf(os.Stderr, "lint OpenAPI document: %v\n", err)
		os.Exit(1)
	}
}

func lint(document *openapi3.T) error {
	var problems []error
	operationIDs := make(map[string]string)

	for path, item := range document.Paths.Map() {
		operations := map[string]*openapi3.Operation{
			"DELETE":  item.Delete,
			"GET":     item.Get,
			"HEAD":    item.Head,
			"OPTIONS": item.Options,
			"PATCH":   item.Patch,
			"POST":    item.Post,
			"PUT":     item.Put,
			"TRACE":   item.Trace,
		}
		for method, operation := range operations {
			if operation == nil {
				continue
			}
			location := method + " " + path
			if operation.OperationID == "" {
				problems = append(problems, fmt.Errorf("%s has no operationId", location))
				continue
			}
			if previous, exists := operationIDs[operation.OperationID]; exists {
				problems = append(problems, fmt.Errorf("operationId %q is shared by %s and %s", operation.OperationID, previous, location))
			}
			operationIDs[operation.OperationID] = location
		}
	}

	for _, schemaName := range []string{"APIError", "ErrorResponse"} {
		if document.Components.Schemas[schemaName] == nil {
			problems = append(problems, fmt.Errorf("components.schemas.%s is required", schemaName))
		}
	}

	sort.Slice(problems, func(i, j int) bool { return problems[i].Error() < problems[j].Error() })
	return errors.Join(problems...)
}
