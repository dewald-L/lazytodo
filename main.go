// Command lazytodo runs the todo plugin on its own: lazytodo [-dir DIR].
// Inside dock, install github.com/dewald-L/lazytodo/dockplugin instead.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dewald-L/dock/plugin"

	"github.com/dewald-L/lazytodo/dockplugin"
)

func main() {
	home, _ := os.UserHomeDir()
	dir := flag.String("dir", filepath.Join(home, ".local", "share", "dock", "lazytodo"), "data directory")
	flag.Parse()
	err := plugin.RunStandalone(dockplugin.Plugin{}, plugin.Context{DataDir: *dir, ConfigDir: *dir})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazytodo:", err)
		os.Exit(1)
	}
}
