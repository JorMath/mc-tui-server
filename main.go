package main

import (
	"fmt"
	"os"

	tui "github.com/grindlemire/go-tui"

	"mc-tui-server/internal/config"
	"mc-tui-server/internal/server"
)

func main() {
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

	application, err := tui.NewApp(tui.WithRootComponent(App(managers)))
	if err != nil {
		return err
	}
	defer application.Close()
	if err := application.Run(); err != nil {
		return err
	}

	// Al salir de la TUI, detener con gracia los servidores que sigan vivos
	// para no dejar procesos java huérfanos.
	for _, m := range managers {
		if m.Status() == server.Running {
			_ = m.Stop(stopTimeout)
		}
	}
	return nil
}
