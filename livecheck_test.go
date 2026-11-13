package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/serverjar"
)

func TestLiveProviderUpdatePath(t *testing.T) {
	if os.Getenv("LIVE_CHECK") == "" {
		t.Skip("set LIVE_CHECK=1 to hit the real provider APIs")
	}
	for _, typ := range []config.ServerType{config.Vanilla, config.Paper, config.Purpur, config.Fabric} {
		t.Run(string(typ), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			p, err := download.For(typ, nil)
			if err != nil {
				t.Fatal(err)
			}
			versions, err := p.Versions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(versions) == 0 {
				t.Fatal("no versions")
			}
			version := versions[0]
			url, err := p.ResolveJarURL(ctx, version)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s %s -> %s", typ, version, url)
			dir := t.TempDir()
			staged, err := serverjar.Stage(filepath.Join(dir, "server.jar"), url,
				func(u, dest string, progress func(done, total int64)) error {
					return download.DownloadFile(ctx, nil, u, dest, progress)
				}, nil)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(staged)
			if err != nil || info.Size() == 0 {
				t.Fatalf("staged jar vacío: %v", err)
			}
			t.Logf("staged %d bytes", info.Size())
			serverjar.Discard(staged)
		})
	}
}
