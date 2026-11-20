// Command aviary-supervisor runs the Supervisor with the plugins listed in
// plugins.go.
package main

import "aviary/supervisor/app"

func main() {
	app.Main(plugins)
}
