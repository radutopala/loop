package mcpbrowser

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// refArg is an element ref from read_page or find. Both list refs as
// "ref_N", so an agent naturally passes that back; it is accepted as well as
// the bare number N.
type refArg int

func (r *refArg) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*r = refArg(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("ref must be a number or \"ref_N\": %s", data)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "ref_"))
	if err != nil {
		return fmt.Errorf("ref must be a number or \"ref_N\", got %q", s)
	}
	*r = refArg(n)
	return nil
}

// refSchema describes refArg: inferred from its kind it would be an integer
// only, and the SDK would reject "ref_3" before the tool ever saw it.
var refSchema = &jsonschema.Schema{
	Types:       []string{"integer", "string"},
	Description: `Element ref from read_page or find: the number N or "ref_N"`,
}

// inputSchemaFor is T's input schema with refArg fields taking either form.
func inputSchemaFor[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[refArg](): refSchema},
	})
	if err != nil {
		panic(fmt.Sprintf("input schema for %T: %v", *new(T), err))
	}
	return schema
}
