// Package rendered reads the output of `helm template`.
package rendered

import (
	"errors"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
)

// Read returns the objects of the rendered chart at path.
func Read(path string) ([]map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var objects []map[string]any
	documents := yaml.NewDecoder(file)
	for {
		var object map[string]any
		err := documents.Decode(&object)
		if errors.Is(err, io.EOF) {
			return objects, nil
		}
		if err != nil {
			return nil, err
		}
		if object != nil {
			objects = append(objects, object)
		}
	}
}

// Find returns the object with the kind and name, or nil.
func Find(objects []map[string]any, kind, name string) map[string]any {
	for _, object := range objects {
		if object["kind"] == kind && At(object, "metadata", "name") == name {
			return object
		}
	}
	return nil
}

// At returns the value at path, which is made of field names and list
// indexes, or nil if there is none.
func At(value any, path ...any) any {
	for _, step := range path {
		switch step := step.(type) {
		case string:
			fields, _ := value.(map[string]any)
			value = fields[step]
		case int:
			items, _ := value.([]any)
			if step >= len(items) {
				return nil
			}
			value = items[step]
		}
	}
	return value
}

// Named returns the item of list whose name field is name, or nil.
func Named(list any, name string) any {
	items, _ := list.([]any)
	for _, item := range items {
		if At(item, "name") == name {
			return item
		}
	}
	return nil
}
