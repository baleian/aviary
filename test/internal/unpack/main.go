// Command unpack writes the Egress Proxy configuration of a rendered chart to
// a directory, as the files that the proxy's pod mounts.
//
//	unpack <rendered chart> <directory>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"aviary/test/internal/rendered"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: unpack <rendered chart> <directory>")
		os.Exit(2)
	}
	if err := unpack(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func unpack(chart, directory string) error {
	objects, err := rendered.Read(chart)
	if err != nil {
		return err
	}
	_, files, err := rendered.Envoy(objects)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
