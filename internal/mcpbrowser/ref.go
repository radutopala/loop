package mcpbrowser

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// refArg is an element ref from find_elements, which lists refs as
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
	Description: `Element ref from find_elements: the number N or "ref_N"`,
}

// inputSchemaFor is T's input schema with refArg, pointArg and numberArg
// fields taking each of their forms.
func inputSchemaFor[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[refArg]():    refSchema,
			reflect.TypeFor[pointArg]():  pointSchema,
			reflect.TypeFor[numberArg](): numberSchema,
		},
	})
	if err != nil {
		panic(fmt.Sprintf("input schema for %T: %v", *new(T), err))
	}
	return schema
}

// pointArg is a coordinate pair, [x, y]. Agents used to the computer-use tool
// send it as coordinate, sometimes as the string "[x, y]".
type pointArg []float64

func (p *pointArg) UnmarshalJSON(data []byte) error {
	data = unquote(data)
	var xy []float64
	if err := json.Unmarshal(data, &xy); err != nil || len(xy) != 2 {
		return fmt.Errorf("coordinate must be [x, y]: %s", data)
	}
	*p = xy
	return nil
}

// pointSchema describes pointArg, which takes the pair as an array or a string.
var pointSchema = &jsonschema.Schema{
	Types:       []string{"array", "string"},
	Items:       &jsonschema.Schema{Type: "number"},
	Description: "[x, y] to act at, the same as x and y",
}

// numberArg is a number that may come quoted, as "10".
type numberArg float64

func (n *numberArg) UnmarshalJSON(data []byte) error {
	data = unquote(data)
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("must be a number: %s", data)
	}
	*n = numberArg(f)
	return nil
}

// numberSchema describes numberArg.
var numberSchema = &jsonschema.Schema{Types: []string{"number", "string"}}

// unquote takes up to two layers of JSON string off data: agents send these
// values as strings, and a value the agent meant as "5" has been seen to
// arrive as the string "\"5\"".
func unquote(data []byte) []byte {
	for range 2 {
		var s string
		if json.Unmarshal(data, &s) != nil {
			break
		}
		data = []byte(strings.TrimSpace(s))
	}
	return data
}
