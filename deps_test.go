package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// fakeDepGraph is a resolver/downloader pair driven by in-memory nodes.
type fakeDepGraph struct {
	nodes       map[string]depNode
	resolveErr  map[string]error
	downloadErr map[string]error
	resolved    []string
	downloaded  []string
	onDownload  func()
}

func (g *fakeDepGraph) resolve(ctx context.Context, ref depRef) (depNode, error) {
	g.resolved = append(g.resolved, ref.Key)
	if err := g.resolveErr[ref.Key]; err != nil {
		return depNode{}, err
	}
	node, ok := g.nodes[ref.Key]
	if !ok {
		return depNode{}, fmt.Errorf("unknown node %s", ref.Key)
	}
	return node, nil
}

func (g *fakeDepGraph) download(ctx context.Context, node depNode, dest string) error {
	if err := g.downloadErr[node.Filename]; err != nil {
		return err
	}
	g.downloaded = append(g.downloaded, node.Filename)
	if g.onDownload != nil {
		g.onDownload()
	}
	return nil
}

func depTestApp(t *testing.T) (*app, config.Instance) {
	t.Helper()
	a, inst, _ := newJavaApp(t)
	return a, inst
}

func depLog(a *app, name string) string {
	return strings.Join(a.logs.Get()[name], "\n")
}

func runGraph(t *testing.T, a *app, inst config.Instance, root depRef, g *fakeDepGraph) int {
	t.Helper()
	return a.installDepGraph(context.Background(), a.mrGen.Get(), inst, "plugins", root, g.resolve, g.download)
}

func TestInstallDepGraphOrderAndDedupe(t *testing.T) {
	a, inst := depTestApp(t)
	g := &fakeDepGraph{nodes: map[string]depNode{
		"root": {Deps: []depRef{{Key: "a", Name: "A"}, {Key: "b", Name: "B"}}},
		"a":    {Filename: "a.jar", URL: "https://example.invalid/a.jar", Deps: []depRef{{Key: "c", Name: "C"}}},
		"b":    {Filename: "b.jar", URL: "https://example.invalid/b.jar", Deps: []depRef{{Key: "c", Name: "C"}}},
		"c":    {Filename: "c.jar", URL: "https://example.invalid/c.jar"},
	}}
	root := depRef{Key: "root", Name: "Root"}
	installed := runGraph(t, a, inst, root, g)

	if installed != 3 {
		t.Fatalf("installed = %d, want 3", installed)
	}
	want := []string{"a.jar", "b.jar", "c.jar"}
	if strings.Join(g.downloaded, ",") != strings.Join(want, ",") {
		t.Fatalf("install order = %v, want %v", g.downloaded, want)
	}
	if n := strings.Count(strings.Join(g.resolved, ","), "c"); n != 1 {
		t.Fatalf("c was resolved %d times, want once (dedupe by key)", n)
	}
}

func TestInstallDepGraphTerminatesOnCycle(t *testing.T) {
	a, inst := depTestApp(t)
	g := &fakeDepGraph{nodes: map[string]depNode{
		"root": {Deps: []depRef{{Key: "a", Name: "A"}}},
		"a":    {Filename: "a.jar", Deps: []depRef{{Key: "b", Name: "B"}}},
		"b":    {Filename: "b.jar", Deps: []depRef{{Key: "a", Name: "A"}, {Key: "root", Name: "Root"}}},
	}}
	installed := runGraph(t, a, inst, depRef{Key: "root", Name: "Root"}, g)
	if installed != 2 {
		t.Fatalf("installed = %d, want 2; the A→B→A cycle must terminate", installed)
	}
	if strings.Join(g.downloaded, ",") != "a.jar,b.jar" {
		t.Fatalf("downloaded = %v", g.downloaded)
	}
}

func TestInstallDepGraphCapsVisitedNodes(t *testing.T) {
	a, inst := depTestApp(t)
	nodes := map[string]depNode{
		"root": {Deps: []depRef{{Key: "n1", Name: "N1"}}},
	}
	for i := 1; i <= 30; i++ {
		node := depNode{Filename: fmt.Sprintf("n%d.jar", i)}
		if i < 30 {
			node.Deps = []depRef{{Key: fmt.Sprintf("n%d", i+1), Name: fmt.Sprintf("N%d", i+1)}}
		}
		nodes[fmt.Sprintf("n%d", i)] = node
	}
	g := &fakeDepGraph{nodes: nodes}
	installed := runGraph(t, a, inst, depRef{Key: "root", Name: "Root"}, g)

	// root + 24 dependencies fit under the 25 visited-node cap.
	if installed != 24 {
		t.Fatalf("installed = %d, want 24 under the 25-node cap", installed)
	}
	if strings.Contains(strings.Join(g.resolved, ","), "n25") {
		t.Fatalf("node n25 was resolved past the cap: %v", g.resolved)
	}
}

func TestInstallDepGraphSkipsInstalledFileButFollowsDeps(t *testing.T) {
	a, inst := depTestApp(t)
	dir := filepath.Join(inst.Dir, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jar"), []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &fakeDepGraph{nodes: map[string]depNode{
		"root": {Deps: []depRef{{Key: "a", Name: "A"}}},
		"a":    {Filename: "a.jar", Deps: []depRef{{Key: "b", Name: "B"}}},
		"b":    {Filename: "b.jar"},
	}}
	installed := runGraph(t, a, inst, depRef{Key: "root", Name: "Root"}, g)

	if installed != 1 {
		t.Fatalf("installed = %d, want only b.jar", installed)
	}
	if strings.Join(g.downloaded, ",") != "b.jar" {
		t.Fatalf("downloaded = %v, a.jar was already on disk", g.downloaded)
	}
}

func TestInstallDepGraphSkipsResolverError(t *testing.T) {
	a, inst := depTestApp(t)
	g := &fakeDepGraph{
		nodes: map[string]depNode{
			"root": {Deps: []depRef{{Key: "bad", Name: "Bad"}, {Key: "good", Name: "Good"}}},
			"good": {Filename: "good.jar"},
		},
		resolveErr: map[string]error{"bad": errors.New("boom")},
	}
	installed := runGraph(t, a, inst, depRef{Key: "root", Name: "Root"}, g)

	if installed != 1 || strings.Join(g.downloaded, ",") != "good.jar" {
		t.Fatalf("installed = %d, downloaded = %v; a broken dependency must not abort the rest", installed, g.downloaded)
	}
	if log := depLog(a, inst.Name); !strings.Contains(log, "Skipped dependency Bad: boom") {
		t.Fatalf("log = %q", log)
	}
}

func TestInstallDepGraphSkipsExternalNode(t *testing.T) {
	a, inst := depTestApp(t)
	g := &fakeDepGraph{nodes: map[string]depNode{
		"root": {Deps: []depRef{{Key: "ext", Name: "Ext"}}},
		"ext":  {Filename: "ext.jar", External: "https://outside.invalid/ext.jar"},
	}}
	installed := runGraph(t, a, inst, depRef{Key: "root", Name: "Root"}, g)

	if installed != 0 || len(g.downloaded) != 0 {
		t.Fatalf("installed = %d, downloaded = %v; an external node must never be fetched", installed, g.downloaded)
	}
	log := depLog(a, inst.Name)
	if !strings.Contains(log, "Skipped dependency Ext: hosted externally (https://outside.invalid/ext.jar)") {
		t.Fatalf("log = %q", log)
	}
}

func TestInstallDepGraphSkipsDownloadError(t *testing.T) {
	a, inst := depTestApp(t)
	g := &fakeDepGraph{
		nodes: map[string]depNode{
			"root": {Deps: []depRef{{Key: "bad", Name: "Bad"}, {Key: "good", Name: "Good"}}},
			"bad":  {Filename: "bad.jar"},
			"good": {Filename: "good.jar"},
		},
		downloadErr: map[string]error{"bad.jar": errors.New("connection reset")},
	}
	installed := runGraph(t, a, inst, depRef{Key: "root", Name: "Root"}, g)

	if installed != 1 || strings.Join(g.downloaded, ",") != "good.jar" {
		t.Fatalf("installed = %d, downloaded = %v", installed, g.downloaded)
	}
	if log := depLog(a, inst.Name); !strings.Contains(log, "Skipped dependency Bad: connection reset") {
		t.Fatalf("log = %q", log)
	}
}

func TestInstallDepGraphStopsWhenGenerationChanges(t *testing.T) {
	a, inst := depTestApp(t)
	g := &fakeDepGraph{
		nodes: map[string]depNode{
			"root": {Deps: []depRef{{Key: "a", Name: "A"}, {Key: "b", Name: "B"}}},
			"a":    {Filename: "a.jar"},
			"b":    {Filename: "b.jar"},
		},
	}
	g.onDownload = func() {
		a.mrGen.Update(func(gen int) int { return gen + 1 })
	}
	installed := runGraph(t, a, inst, depRef{Key: "root", Name: "Root"}, g)

	if installed != 1 || strings.Join(g.downloaded, ",") != "a.jar" {
		t.Fatalf("installed = %d, downloaded = %v; a closed panel must stop the walk", installed, g.downloaded)
	}
}
