package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"
)

// newSchemaReflector returns a reflector configured for phosphor's
// configuration types. csync.Map is a locked wrapper around a plain map;
// Map instances carry a sync.RWMutex, so they cannot expose a value-receiver
// JSONSchemaAlias without tripping go vet's copylocks check. The schema is
// mapped here instead.
func newSchemaReflector() *jsonschema.Reflector {
	reflector := new(jsonschema.Reflector)
	reflector.Mapper = func(t reflect.Type) *jsonschema.Schema {
		if t.Kind() == reflect.Struct && t.PkgPath() == "github.com/hackafterdark/phosphor/pkg/csync" &&
			strings.HasPrefix(t.Name(), "Map[") {
			return &jsonschema.Schema{
				Type: "object",
				AdditionalProperties: &jsonschema.Schema{
					Ref: "#/$defs/ProviderConfig",
				},
			}
		}
		return nil
	}
	return reflector
}

// newConfigSchema reflects config.Config and adds the ProviderConfig
// definition that the providers mapper references, promoting any nested
// definitions it pulls in to the root so their $refs resolve too.
func newConfigSchema() *jsonschema.Schema {
	root := newSchemaReflector().Reflect(&config.Config{})

	expanded := newSchemaReflector()
	expanded.ExpandedStruct = true
	providerSchema := expanded.Reflect(&config.ProviderConfig{})
	providerSchema.ID = jsonschema.EmptyID
	providerSchema.Version = ""

	root.Definitions["ProviderConfig"] = providerSchema
	for name, def := range providerSchema.Definitions {
		if _, exists := root.Definitions[name]; !exists {
			root.Definitions[name] = def
		}
	}
	providerSchema.Definitions = nil
	return root
}

var schemaCmd = &cobra.Command{
	Use:    "schema",
	Short:  "Generate JSON schema for configuration",
	Long:   "Generate JSON schema for the phosphor configuration file",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		bts, err := json.MarshalIndent(newConfigSchema(), "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal schema: %w", err)
		}
		fmt.Println(string(bts))
		return nil
	},
}
