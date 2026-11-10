package web

import (
	"io/fs"
	"testing"
)

func TestFS(t *testing.T) {
	ui := FS()
	if _, err := fs.Stat(ui, ".gitkeep"); err != nil {
		t.Fatalf("dist/.gitkeep must stay embedded so a clean checkout compiles: %v", err)
	}
	if !Built() {
		t.Skip("web UI not built; run make build-web")
	}
	data, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("index.html is empty")
	}
}
