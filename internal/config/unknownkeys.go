package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Unknown-key detection. Config loading decodes into typed structs, which
// silently drops any key the schema does not declare — and the next save
// re-marshals from the struct, permanently erasing it. That makes a typo such
// as `container_host_gatway: false` doubly bad: it never takes effect AND
// disappears from the file without a trace. Loading therefore reports unknown
// keys as non-fatal warnings. It must not reject them: a config written by a
// NEWER launcher has to keep loading on an older one, so hard-fail strict
// decoding is off the table by design.
//
// The known-key set is derived from the structs' own yaml tags by reflection,
// so it can never drift into a third copy of the schema. Nested structs,
// slices of structs (agents, mounts) and maps of structs (profiles) are
// walked recursively; free-form maps (permissions, param_values, container
// environment) accept any key below them.

// unknownKeyWarnings returns one human-readable warning per unknown key found
// in data when read as a document of the kind sample declares. Each warning
// names the file and the dotted key path (for example
// `options.container_host_gatway`). The paths are sorted for stable output.
func unknownKeyWarnings(label, path string, data []byte, sample any) []string {
	keys := unknownKeysIn(data, sample)
	if len(keys) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(keys))
	for _, key := range keys {
		warnings = append(warnings, fmt.Sprintf(
			"%s %s declares unknown key %q; the value is ignored and the next save drops it — check for a typo or a launcher upgrade",
			label, path, key))
	}
	return warnings
}

// unknownKeysIn decodes data as a plain YAML mapping and returns the dotted
// paths of keys that the type of sample does not declare. A document that is
// not a mapping (or does not parse) yields nothing: parse failures are
// already reported by the primary decode.
func unknownKeysIn(data []byte, sample any) []string {
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil || document == nil {
		return nil
	}
	var unknown []string
	walkUnknownKeys("", document, derefType(reflect.TypeOf(sample)), &unknown)
	sort.Strings(unknown)
	return unknown
}

// walkUnknownKeys compares the mapping against the yaml tags of structType,
// recursing into nested struct fields, slices of structs and maps of structs.
func walkUnknownKeys(prefix string, document map[string]any, structType reflect.Type, unknown *[]string) {
	fields := knownYAMLFields(structType)
	for key, value := range document {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		fieldType, known := fields[key]
		if !known {
			*unknown = append(*unknown, path)
			continue
		}
		walkKnownValue(path, value, derefType(fieldType), unknown)
	}
}

// walkKnownValue recurses into the value of a key the schema declares when
// the declared type is itself a mapping-shaped structure. Scalars and
// free-form maps (map[string]string, map[string]bool, …) accept anything.
func walkKnownValue(path string, value any, fieldType reflect.Type, unknown *[]string) {
	switch fieldType.Kind() {
	case reflect.Struct:
		if nested, ok := value.(map[string]any); ok {
			walkUnknownKeys(path, nested, fieldType, unknown)
		}
	case reflect.Slice:
		elem := derefType(fieldType.Elem())
		if elem.Kind() != reflect.Struct {
			return
		}
		items, ok := value.([]any)
		if !ok {
			return
		}
		for index, item := range items {
			if nested, ok := item.(map[string]any); ok {
				walkUnknownKeys(fmt.Sprintf("%s[%d]", path, index), nested, elem, unknown)
			}
		}
	case reflect.Map:
		elem := derefType(fieldType.Elem())
		if elem.Kind() != reflect.Struct {
			return
		}
		nested, ok := value.(map[string]any)
		if !ok {
			return
		}
		for mapKey, item := range nested {
			if itemMap, ok := item.(map[string]any); ok {
				walkUnknownKeys(path+"."+mapKey, itemMap, elem, unknown)
			}
		}
	}
}

// knownYAMLFields maps each exported field's yaml name to its type. The tag
// name is authoritative; a field without a tag falls back to the lowercased
// field name, matching the decoder's default. `-` fields are skipped because
// they never appear in a document.
func knownYAMLFields(structType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if field.PkgPath != "" { // unexported
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		fields[name] = field.Type
	}
	return fields
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
