// Command openapi prints the API's OpenAPI 3.1 document as YAML, from the same route registration
// as `arch-api serve`, without a database or Keycloak. `make openapi` writes it to api/openapi.yaml.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"

	"github.com/CliXiD/arch-api/internal/httpapi"
)

// leading are the top-level keys written first, in this order; the rest follow sorted.
var leading = []string{"openapi", "info", "paths", "components"}

func main() {
	gin.SetMode(gin.ReleaseMode)
	api := httpapi.NewAPI(gin.New(), httpapi.Deps{})
	out, err := render(api.OpenAPI())
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		os.Exit(1)
	}
}

// render marshals the document deterministically, starting with `openapi: 3.1.0`.
func render(doc any) ([]byte, error) {
	spec, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var top yaml.MapSlice
	if err := yaml.UnmarshalWithOptions(spec, &top, yaml.UseOrderedMap()); err != nil {
		return nil, err
	}
	slices.SortStableFunc(top, func(a, b yaml.MapItem) int {
		return rank(a.Key) - rank(b.Key)
	})
	return yaml.Marshal(top)
}

func rank(key any) int {
	if i := slices.Index(leading, fmt.Sprint(key)); i >= 0 {
		return i
	}
	return len(leading)
}
