package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRevisionPagingAndDiffEdgesQA(t *testing.T) {
	e := newEnv(t, true)
	adm := e.admin()
	checkPage := func(query string, ids []int64, more bool) {
		t.Helper()
		code, body := adm.do("GET", "/api/v1/revisions"+query, nil)
		var page struct {
			Items []revisionInfo
			Next  *string
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		got := []int64{}
		for _, r := range page.Items {
			got = append(got, r.ID)
		}
		if code != 200 || page.Items == nil || !reflect.DeepEqual(got, ids) || (page.Next != nil) != more {
			t.Fatalf("page %s: %d %s", query, code, body)
		}
	}
	checkPage("", []int64{}, false)
	e.seed(adm, "web1")
	e.approve(adm, "two", targetDoc("web1", "two"))
	checkPage("?limit=1", []int64{2}, true)
	checkPage("?limit=2", []int64{2, 1}, false)
	checkPage("?limit=200", []int64{2, 1}, false)
	checkPage("?cursor="+*encodeCursor("2"), []int64{1}, false)
	checkPage("?cursor="+*encodeCursor("1"), []int64{}, false)
	for _, q := range []string{"?limit=0", "?limit=201", "?limit=x", "?cursor=!", "?cursor=" + *encodeCursor("x")} {
		if code, body := adm.do("GET", "/api/v1/revisions"+q, nil); code != 400 {
			t.Errorf("%s: %d %s", q, code, body)
		}
	}
	for _, n := range []string{"1", "2"} {
		code, body := adm.do("GET", "/api/v1/revisions/"+n+"/diff?from="+n, nil)
		var diff revisionDiff
		if err := json.Unmarshal(body, &diff); err != nil {
			t.Fatal(err)
		}
		if code != 200 || diff.From == nil || itoa(*diff.From) != n || itoa(diff.To) != n || diff.Changes == nil || len(diff.Changes) != 0 {
			t.Errorf("self diff: %d %s", code, body)
		}
	}
	code, body := adm.do("GET", "/api/v1/revisions/1/diff?from=2", nil)
	var diff revisionDiff
	if err := json.Unmarshal(body, &diff); err != nil {
		t.Fatal(err)
	}
	if code != 200 || diff.From == nil || *diff.From != 2 || diff.To != 1 || len(diff.Changes) != 1 {
		t.Fatalf("reverse diff: %d %s", code, body)
	}
	c := diff.Changes[0]
	for _, pair := range []struct {
		rev  int64
		body json.RawMessage
	}{{2, c.Before}, {1, c.After}} {
		d, err := e.st.Doc(e.ctx, e.hashes(pair.rev)[c.Kind+"/"+c.Name])
		if err != nil {
			t.Fatal(err)
		}
		if string(d.Body) != string(pair.body) {
			t.Errorf("diff body differs from stored document: %s", pair.body)
		}
	}
}
