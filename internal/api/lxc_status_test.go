package api

import (
	"encoding/json"
	"errors"
	"github.com/Bernotese/plumb/internal/store"
	"strings"
	"testing"

	"github.com/Bernotese/plumb/internal/driver"
	"github.com/Bernotese/plumb/internal/driver/fake"
	"github.com/Bernotese/plumb/internal/state"
	"github.com/Bernotese/plumb/internal/wire"
)

// T-085, D-233: every lxc Image has a status; unused_imports lists its
// unused template files with the pvesm line, not the file of the current
// document and not a file that a container uses.
func TestLXCImageStatusUnusedTemplateFiles(t *testing.T) {
	e := newEnv(t, false)
	adm := e.admin()
	const url = "http://download.proxmox.com/a.tar.zst"
	spec := func(pin string) driver.ImageSpec {
		return driver.ImageSpec{Name: "ct", Kind: wire.KindLXC, URL: url, SHA512: strings.Repeat(pin, 128)}
	}
	doc, _ := json.Marshal(map[string]any{"type": "lxc", "url": url, "sha512": strings.Repeat("c", 128)})
	e.approve(adm, "lxc image", state.Doc{Kind: state.KindImage, Name: "ct", Spec: doc})

	type view struct {
		Status *struct {
			Versions      []any `json:"versions"`
			UnusedImports []struct {
				Volume    string `json:"volume"`
				SizeBytes int64  `json:"size_bytes"`
				Command   string `json:"command"`
			} `json:"unused_imports"`
		} `json:"status"`
	}
	get := func() view {
		t.Helper()
		code, body := adm.do("GET", "/api/v1/images/ct", nil)
		var v view
		if code != 200 || json.Unmarshal(body, &v) != nil || v.Status == nil || v.Status.Versions == nil || v.Status.UnusedImports == nil {
			t.Fatalf("image status: %d %s", code, body)
		}
		return v
	}
	if v := get(); len(v.Status.UnusedImports) != 0 {
		t.Fatalf("no files yet: %+v", v.Status.UnusedImports)
	}

	refs := map[string]driver.ImageRef{}
	for _, pin := range []string{"a", "b", "c"} {
		ref, err := e.drv.FetchImage(e.ctx, spec(pin))
		if err != nil {
			t.Fatal(err)
		}
		refs[pin] = ref
	}
	if _, err := e.drv.Create(e.ctx, driver.GuestSpec{Name: "c1", Kind: wire.KindLXC, ImageName: "ct", Image: refs["b"],
		Cores: 1, MemoryMB: 512, DiskGB: 4, Bridge: "vmbr0", IP: "dhcp"}, driver.Bootstrap{Token: "t"}); err != nil {
		t.Fatal(err)
	}
	got := get().Status.UnusedImports
	if len(got) != 1 || got[0].Volume != string(refs["a"]) || got[0].Command != "pvesm free "+string(refs["a"]) || got[0].SizeBytes <= 0 {
		t.Fatalf("unused = %+v, want only %s", got, refs["a"])
	}

	// The list of all Images has the same status.
	code, body := adm.do("GET", "/api/v1/images", nil)
	var listed struct {
		Items []view `json:"items"`
	}
	if code != 200 || json.Unmarshal(body, &listed) != nil || len(listed.Items) != 1 || listed.Items[0].Status == nil {
		t.Fatalf("image list: %d %s", code, body)
	}
	files := listed.Items[0].Status.UnusedImports
	if len(files) != 1 || files[0].Volume != string(refs["a"]) || files[0].Command != "pvesm free "+string(refs["a"]) {
		t.Fatalf("image list unused files: %+v", files)
	}

	// Delete the document through an approved Run, keeping every template file.
	before := e.drv.Calls(fake.OpRemoveImage)
	e.runPlan(adm, map[string]any{"reason": "delete lxc image", "delete": []map[string]string{{"kind": "Image", "name": "ct"}}})
	if n := e.drv.Calls(fake.OpRemoveImage) - before; n != 0 {
		t.Fatalf("Image delete called RemoveImage %d times", n)
	}
	if files, err := e.drv.ListImages(e.ctx, "ct"); err != nil || len(files) != 3 {
		t.Fatalf("files after Image delete: %+v, %v", files, err)
	}
	e.approve(adm, "restore lxc image", state.Doc{Kind: state.KindImage, Name: "ct", Spec: doc})

	// A driver error leaves the list empty; the status is still there.
	e.drv.Fail(fake.OpListImages, errors.New("storage down"))
	if v := get(); len(v.Status.UnusedImports) != 0 {
		t.Fatalf("list error: %+v", v.Status.UnusedImports)
	}
}

// Status exists for stock and built LXC Images, even without any files or versions.
func TestLXCImageStatusWithAndWithoutBuild(t *testing.T) {
	e := newEnv(t, false)
	for _, build := range []string{"", `,"build":null`, `,"build":{"blocks":[]}`} {
		t.Run(build, func(t *testing.T) {
			body := `{"spec":{"type":"lxc","url":"http://download.proxmox.com/a.tar.zst","sha512":"` + strings.Repeat("c", 128) + `"` + build + `}}`
			status, err := e.srv.imageStatus(e.ctx, store.DocRow{Name: "ct", Body: json.RawMessage(body)})
			if err != nil || status == nil || status.Versions == nil || status.UnusedImports == nil {
				t.Fatalf("status = %+v, %v", status, err)
			}
		})
	}
}
