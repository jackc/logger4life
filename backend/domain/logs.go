// Package domain contains Logger4Life's pure business types and rules.
package domain

import (
	"fmt"
	"strconv"
	"strings"
)

type FieldDefinition struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

func ValidateFieldDefinitions(fields []FieldDefinition) error {
	if len(fields) > 20 {
		return fmt.Errorf("too many fields (max 20)")
	}
	seen := map[string]bool{}
	for i := range fields {
		fields[i].Name = strings.TrimSpace(fields[i].Name)
		f := fields[i]
		if f.Name == "" || len(f.Name) > 100 {
			return fmt.Errorf("field name must be 1-100 characters")
		}
		key := strings.ToLower(f.Name)
		if seen[key] {
			return fmt.Errorf("duplicate field name: %s", f.Name)
		}
		seen[key] = true
		if f.Type != "text" && f.Type != "number" && f.Type != "boolean" {
			return fmt.Errorf("field type must be 'text', 'number', or 'boolean'")
		}
	}
	return nil
}

func ValidateFieldValues(defs []FieldDefinition, values map[string]any) error {
	defMap := map[string]FieldDefinition{}
	for _, d := range defs {
		defMap[d.Name] = d
	}
	for name := range values {
		if _, ok := defMap[name]; !ok {
			return fmt.Errorf("unknown field: %s", name)
		}
	}
	for _, d := range defs {
		v, ok := values[d.Name]
		if !ok || v == nil {
			if d.Required {
				return fmt.Errorf("field %q is required", d.Name)
			}
			continue
		}
		switch d.Type {
		case "text", "number":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("field %q must be a string", d.Name)
			}
			if d.Required && strings.TrimSpace(s) == "" {
				return fmt.Errorf("field %q is required", d.Name)
			}
			if d.Type == "number" && strings.TrimSpace(s) != "" {
				if _, err := strconv.ParseFloat(s, 64); err != nil {
					return fmt.Errorf("field %q must be a valid number", d.Name)
				}
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("field %q must be true or false", d.Name)
			}
		}
	}
	return nil
}
