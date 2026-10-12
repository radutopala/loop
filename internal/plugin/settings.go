package plugin

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/radutopala/loop/internal/config"
)

// jsonSchema is p as a JSON schema, with the keywords a value is checked
// against.
func jsonSchema(p *config.SchemaProperty) *jsonschema.Schema {
	if p == nil {
		return nil
	}
	s := &jsonschema.Schema{
		Type: p.Type, Enum: p.Enum, Minimum: p.Minimum, Maximum: p.Maximum, Pattern: p.Pattern,
		Items: jsonSchema(p.Items), AdditionalProperties: jsonSchema(p.AdditionalProperties),
	}
	for name, sub := range p.Properties {
		if s.Properties == nil {
			s.Properties = map[string]*jsonschema.Schema{}
		}
		s.Properties[name] = jsonSchema(sub)
	}
	return s
}

// checkSetting checks v against the setting name's schema.
func (m *Manifest) checkSetting(name string, v any) error {
	p := m.Settings[name]
	if p == nil {
		return fmt.Errorf("setting %q: the plugin has no such setting", name)
	}
	// The schema's own errors print bounds as fractions: these read plainly.
	if n, ok := v.(float64); ok {
		if p.Minimum != nil && n < *p.Minimum {
			return fmt.Errorf("setting %q: %v is below the minimum, %v", name, n, *p.Minimum)
		}
		if p.Maximum != nil && n > *p.Maximum {
			return fmt.Errorf("setting %q: %v is above the maximum, %v", name, n, *p.Maximum)
		}
	}
	rs, err := jsonSchema(p).Resolve(nil)
	if err != nil {
		return fmt.Errorf("setting %q: its schema: %w", name, err)
	}
	if err := rs.Validate(v); err != nil {
		return fmt.Errorf("setting %q: %s", name, strings.TrimPrefix(err.Error(), "validating root: "))
	}
	return nil
}

// ResolveSettings returns the plugin's settings: each setting's default,
// overridden by the values of layers in order. A value for a setting the
// manifest doesn't declare, or one its schema refuses, is left out and
// reported in problems.
func (m *Manifest) ResolveSettings(layers ...map[string]any) (settings map[string]any, problems []string) {
	settings = map[string]any{}
	for name, p := range m.Settings {
		if p != nil && p.Default != nil {
			settings[name] = p.Default
		}
	}
	for _, layer := range layers {
		for _, name := range slices.Sorted(maps.Keys(layer)) {
			if err := m.checkSetting(name, layer[name]); err != nil {
				problems = append(problems, err.Error())
				continue
			}
			settings[name] = layer[name]
		}
	}
	return settings, problems
}
