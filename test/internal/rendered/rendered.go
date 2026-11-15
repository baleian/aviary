// Package rendered reads what `helm template` makes of the chart.
package rendered

import (
	"errors"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
)

// Read returns the objects of the rendered chart in the file.
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

// Find returns the object of the kind with the name, or nothing if the chart
// made none.
func Find(objects []map[string]any, kind, name string) map[string]any {
	for _, object := range objects {
		if object["kind"] == kind && At(object, "metadata", "name") == name {
			return object
		}
	}
	return nil
}

// At returns what a value holds at the path, which is made of the names of
// fields and the indexes of list items, or nothing if it holds nothing there.
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

// Named returns the item of the list whose name is the one given, or
// nothing.
func Named(list any, name string) any {
	items, _ := list.([]any)
	for _, item := range items {
		if At(item, "name") == name {
			return item
		}
	}
	return nil
}

// Envoy returns the configuration of the Egress Proxy that the chart made,
// and the files that hold it by their names.
func Envoy(objects []map[string]any) (map[string]any, map[string]string, error) {
	files := map[string]string{}
	data, _ := At(Find(objects, "ConfigMap", "aviary-egress-proxy"), "data").(map[string]any)
	for name, content := range data {
		files[name], _ = content.(string)
	}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(files["envoy.yaml"]), &config); err != nil {
		return nil, nil, err
	}
	if config == nil {
		return nil, nil, errors.New("the chart made no configuration for the Egress Proxy")
	}
	return config, files, nil
}
