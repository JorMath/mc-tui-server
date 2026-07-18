package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
	tui "github.com/grindlemire/go-tui"
)

// TestBindAppRegistraTodosLosStates protege contra el bug del splash
// congelado (v0.1.1): la struct app vive fuera de app.gsx, así que el
// generador no vincula sus States a la tui.App. Todo campo State debe
// crearse con newState para quedar registrado en binders; si este test
// falla, un State nuevo se creó con tui.NewState y sus Set() no
// re-renderizarán la UI.
func TestBindAppRegistraTodosLosStates(t *testing.T) {
	store := config.NewStore(filepath.Join(t.TempDir(), "instances.json"))
	a := App(store, nil)

	binderIface := reflect.TypeOf((*tui.AppBinder)(nil)).Elem()
	typ := reflect.TypeOf(*a)
	want := 0
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Name == "binders" {
			continue
		}
		if f.Type.Implements(binderIface) {
			want++
		}
	}
	if got := len(a.binders); got != want {
		t.Fatalf("binders registrados = %d, campos State en app = %d; "+
			"algún State no se creó con newState()", got, want)
	}
}
