// deps.go: one source-agnostic walker installs the required dependency graph
// of a freshly installed project. Modrinth and Hangar plug into it through
// small adapters, so the two sources cannot drift apart.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// maxDependencyNodes caps how many nodes one install may visit, the root
// included. No reasonable dependency tree goes past 25.
const maxDependencyNodes = 25

// depRef identifies one node of a dependency graph.
type depRef struct {
	Key  string // source-scoped identity: "hangar:12", Modrinth project id
	Name string // name declared by the parent project, for messages
}

// depNode is a resolved, installable node.
type depNode struct {
	Filename string
	URL      string
	SHA256   string   // verified by the source downloader when non-empty
	Version  string   // version label, for messages
	External string   // non-empty when the node cannot be downloaded
	Deps     []depRef // further required nodes
}

type depResolver func(ctx context.Context, ref depRef) (depNode, error)
type depDownloader func(ctx context.Context, node depNode, dest string) error

// installDepGraph walks the required-dependency graph rooted at root,
// installing every node it has not seen yet. It never installs the root
// itself (the caller already did). Resolution and download failures are
// logged and skipped: a broken dependency never aborts the parent install.
//
// The root is resolved like any other node so its dependencies are
// discovered; adapters answer that one call from data they already hold
// instead of fetching the root version again.
func (a *app) installDepGraph(ctx context.Context, gen int, inst config.Instance, sub string, root depRef, resolve depResolver, download depDownloader) int {
	visited := map[string]bool{root.Key: true}
	queue := []depRef{root}
	enqueue := func(list []depRef) {
		for _, ref := range list {
			if len(visited) >= maxDependencyNodes {
				return
			}
			if ref.Key == "" || visited[ref.Key] {
				continue
			}
			visited[ref.Key] = true
			queue = append(queue, ref)
		}
	}
	installed := 0
	for len(queue) > 0 {
		if a.mrGen.Get() != gen {
			return installed
		}
		ref := queue[0]
		queue = queue[1:]
		node, err := resolve(ctx, ref)
		if err != nil {
			a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Skipped dependency %s: %s", ref.Name, err))
			continue
		}
		// Enqueue before the installed check: an installed jar still
		// contributes its own dependencies.
		enqueue(node.Deps)
		if ref.Key == root.Key {
			continue
		}
		if node.External != "" {
			a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Skipped dependency %s: hosted externally (%s)", ref.Name, node.External))
			continue
		}
		if node.Filename == "" {
			a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Skipped dependency %s: no downloadable file", ref.Name))
			continue
		}
		dest := filepath.Join(inst.Dir, sub, node.Filename)
		if _, statErr := os.Stat(dest); statErr == nil {
			continue // already installed
		}
		if a.mrGen.Get() != gen {
			return installed
		}
		if err := download(ctx, node, dest); err != nil {
			a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Skipped dependency %s: %s", ref.Name, err))
			continue
		}
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed dependency %s", node.Filename))
		installed++
	}
	return installed
}
