package secret

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// T-243 (D-431): no fmt verb, no slog handler and no JSON encoding of a
// Key shows its bytes, in any usual form.
func TestKeyNeverPrintsBytes(t *testing.T) {
	k := NewKey()
	raw := k.b[:]
	needles := []string{string(raw), hex.EncodeToString(raw), strings.ToUpper(hex.EncodeToString(raw)),
		base64.StdEncoding.EncodeToString(raw), base64.RawStdEncoding.EncodeToString(raw), base64.URLEncoding.EncodeToString(raw),
		strings.Trim(fmt.Sprint(raw), "[]"), fmt.Sprintf("%#v", k.b)[len("[32]uint8{"):60]}
	type holder struct{ Key Key }
	var out []string
	for _, v := range []any{k, &k, holder{k}, &holder{k}, []Key{k}, map[string]Key{"k": k}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%o", "%b", "%c", "%U"} {
			out = append(out, fmt.Sprintf(verb, v))
		}
		out = append(out, fmt.Sprint(v), fmt.Sprintln(v))
		var buf bytes.Buffer
		slog.New(slog.NewTextHandler(&buf, nil)).Info("m", "k", v)
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("m", "k", v)
		out = append(out, buf.String())
		if b, err := json.Marshal(v); err == nil {
			out = append(out, string(b))
		}
	}
	for _, o := range out {
		for _, n := range needles {
			if strings.Contains(o, n) {
				t.Fatalf("the key bytes are in %q", o)
			}
		}
	}
	if !strings.Contains(fmt.Sprint(k), redactedKey) {
		t.Fatal("no redaction mark")
	}
}
