package state

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Bernotese/plumb/internal/resource"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// Plain messages for schema rules whose library text names no attribute
// and no rule, like "'not' failed" (T-134 F9). The key is the schema
// location of the failing keyword and the keyword: "<$id>#<pointer> not".

// notPatterns are the messages of the common "not": {"pattern": ...}
// rules, by pattern. They apply wherever the rule is.
var notPatterns = map[string]string{
	`(^|/)\.\.(/|$)|\x00`: "must not contain a .. component or a NUL character",
	`\x00`:                "must not contain a NUL character",
	`^plumb-agent`:        "must not start with plumb-agent: the units of the agent belong to Plumb (D-274)",
	`PRIVATE KEY BLOCK|-----END PGP PUBLIC KEY BLOCK-----[\s\S]*-----BEGIN`: "must hold exactly one public key block and no private key",
}

// ruleMessages are the messages of the other rules, by schema location
// (relative to https://plumb.invalid/schema/) and keyword.
var ruleMessages = map[string]string{
	"resource/firewall.json#/allOf/0/then not":                "allow_no_ssh is true: ssh_port and ssh_sources must then be missing (there is no SSH rule)",
	"resource/firewall.json#/allOf/1/then/propertyNames enum": "state is absent: only name and state are allowed, the other attributes must be missing",
	// The library text repeats the value: never a key in an error.
	"resource/apt_repository.json#/properties/key pattern":                                                         "must be exactly one ASCII-armored public key block",
	"doc/target.json#/properties/spec/properties/guest/dependentSchemas/gateway required":                          "gateway needs a static ip: set ip too",
	"doc/target.json#/properties/spec/properties/guest/dependentSchemas/gateway/properties/ip not":                 "gateway needs a static ip, not dhcp",
	"doc/image.json#/properties/spec/properties/build/properties/guest/dependentSchemas/gateway required":          "gateway needs a static ip: set ip too",
	"doc/image.json#/properties/spec/properties/build/properties/guest/dependentSchemas/gateway/properties/ip not": "gateway needs a static ip, not dhcp",
	"doc/image.json#/properties/spec/properties/schedule/dependentSchemas/weekday/properties/every const":          "weekday needs every: week",
	"doc/image.json#/properties/spec/properties/schedule/dependentSchemas/day/properties/every const":              "day needs every: month",
}

const schemaBase = "https://plumb.invalid/schema/"

// schemaMessages maps "<$id>#<pointer> <keyword>" to its message. Every
// "not" in the schemas of the documents and resource types must have one:
// a new rule without a message is a build mistake (the tests catch it).
var schemaMessages = sync.OnceValues(func() (map[string]string, error) {
	out := map[string]string{}
	var raws [][]byte
	for _, k := range Kinds {
		raw, _ := Schema(k)
		raws = append(raws, raw)
	}
	for _, name := range resource.Names() {
		typ, _ := resource.Lookup(name)
		raws = append(raws, typ.Schema())
	}
	var missing []string
	locs := map[string]map[string]any{} // schema location → object
	for _, raw := range raws {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		id, _ := doc["$id"].(string)
		var walk func(v any, loc string)
		walk = func(v any, loc string) {
			switch v := v.(type) {
			case map[string]any:
				locs[id+"#"+loc] = v
				if not, ok := v["not"]; ok {
					key := id + "#" + loc + " not"
					msg := ruleMessages[strings.TrimPrefix(key, schemaBase)]
					if sub, _ := not.(map[string]any); msg == "" && len(sub) == 1 {
						p, _ := sub["pattern"].(string)
						msg = notPatterns[p]
					}
					if msg == "" {
						missing = append(missing, key)
					}
					out[key] = msg
				}
				for k, c := range v {
					walk(c, ptr(loc, k))
				}
			case []any:
				for i, c := range v {
					walk(c, ptr(loc, i))
				}
			}
		}
		walk(doc, "")
	}
	for key, msg := range ruleMessages {
		loc, keyword, _ := strings.Cut(schemaBase+key, " ")
		if _, ok := locs[loc][keyword]; !ok {
			missing = append(missing, "no rule at "+key)
			continue
		}
		out[schemaBase+key] = msg
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("state: schema messages: %s", strings.Join(missing, ", "))
	}
	return out, nil
})

// ruleMessage returns the plain message of a failed schema keyword, or "".
func ruleMessage(e *jsonschema.ValidationError, keywordLocation string) string {
	msgs, err := schemaMessages()
	if err != nil {
		panic(err) // build mistake; the tests catch it
	}
	keyword := strings.TrimPrefix(keywordLocation, "/")
	if _, ok := e.ErrorKind.(*kind.Not); ok {
		keyword = "not"
	}
	return msgs[e.SchemaURL+" "+keyword]
}
