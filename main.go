package main

import (
	"flag"
	"fmt"
	"os"

	tui "github.com/grindlemire/go-tui"

	"mc-tui-server/internal/config"
	"mc-tui-server/internal/server"
)

// version se inyecta en el build con -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("mc-tui-server", version)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	store := config.NewStore(path)
	if err := store.Load(); err != nil {
		return err
	}

	var managers []*server.Manager
	for _, inst := range store.Instances() {
		managers = append(managers, server.New(inst))
	}

	root := App(store, managers)
	application, err := tui.NewApp(tui.WithRootComponent(root))
	if err != nil {
		return err
	}
	defer application.Close()
	if err := application.Run(); err != nil {
		return err
	}

	// Al salir de la TUI, detener con gracia los servidores que sigan vivos
	// (incluidos los creados por el asistente) para no dejar procesos java
	// huérfanos.
	for _, m := range root.managers.Get() {
		if m.Status() == server.Running {
			_ = m.Stop(stopTimeout)
		}
	}
	return nil
}
