package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Bernotese/plumb/internal/wire"
)

// ErrNotBuilt: the Image has no build part; it is a stock image (D-197).
var ErrNotBuilt = errors.New("state: the Image has no build; it is a stock image")

// Recipe is what a build of an Image makes and how (T-039 Plan §1). A
// build Plan is stale when the hash at the head differs (D-199), and the
// Run uses the head values, so every value that the Run uses is in it:
// also all of build.guest (cores, memory, bridge, ip, gateway: review S2
// of T-059). The agent version is not in it: a build installs the agent
// of the controller (D-199).
type Recipe struct {
	Type      wire.Kind       `json:"type"`
	URL       string          `json:"url"`
	SHA256    string          `json:"sha256"`
	Upgrade   bool            `json:"upgrade"`
	DiskGB    int             `json:"disk"`
	Guest     map[string]any  `json:"guest"` // build.guest with its defaults
	Resources []wire.Resource `json:"resources"`
}

// buildGuestKeys are the settings of build.guest (image.json).
var buildGuestKeys = []string{"cores", "memory", "disk", "bridge", "ip", "gateway"}

// Hash is the hex SHA-256 of the canonical JSON of the recipe. The
// attribute maps are written with sorted keys, so the hash is stable.
func (r Recipe) Hash() string {
	if r.Resources == nil {
		r.Resources = []wire.Resource{}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		panic(err) // rendered values are JSON values
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// RenderedImage is the flat result of the build of one Image.
type RenderedImage struct {
	Image string `json:"image"`
	// Guest is the build guest as a guest:config resource, with the
	// defaults of the build guest and of a Target guest.
	Guest wire.Resource `json:"guest"`
	// Resources are what the build applies, as for a Target. None has a
	// secret (D-202) or touches the agent's paths (D-207).
	Resources []wire.Resource `json:"resources"`
	Recipe    Recipe          `json:"recipe"`
	// RecipeSHA256 is Recipe.Hash().
	RecipeSHA256 string `json:"recipe_sha256"`
}

// BuiltImages returns the names of the Images with a build part, sorted.
func (s *Set) BuiltImages() []string {
	var out []string
	for _, name := range sortedKeys(s.images) {
		if s.images[name].Build != nil {
			out = append(out, name)
		}
	}
	return out
}

// RenderImage renders the build of one Image into its flat resource list
// and its recipe. It returns ErrNotBuilt for a stock Image. The limits of
// the Set apply; the error is of type Errors, or ErrNotBuilt.
func (s *Set) RenderImage(name string) (*RenderedImage, error) {
	img, ok := s.images[name]
	if !ok {
		return nil, Errors{{Kind: KindImage, Name: name, Msg: "Image does not exist"}}
	}
	u, ok := s.imageUnit(name)
	if !ok {
		return nil, ErrNotBuilt
	}
	r, err := s.renderUnit(u, nil)
	if err != nil {
		return nil, err
	}
	disk, _ := intOf(r.Guest.Attrs["disk"])
	out := &RenderedImage{
		Image:     name,
		Guest:     r.Guest,
		Resources: r.Resources,
		Recipe: Recipe{
			Type:      wire.Kind(img.Type),
			URL:       img.URL,
			SHA256:    img.SHA256,
			Upgrade:   img.Build.Upgrade == nil || *img.Build.Upgrade,
			DiskGB:    disk,
			Guest:     buildGuest(r.Guest.Attrs),
			Resources: r.Resources,
		},
	}
	out.RecipeSHA256 = out.Recipe.Hash()
	return out, nil
}

// buildGuest keeps the build.guest settings of the rendered guest:config.
func buildGuest(attrs map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range buildGuestKeys {
		if v, ok := attrs[k]; ok {
			out[k] = v
		}
	}
	return out
}

// DefaultKeep is the default of retention.keep (D-197).
const DefaultKeep = 3

// ImageInfo is what the engine needs to know about an Image besides its
// render: whether it is built, its base checksum, how many versions stay
// and its schedule (canonical JSON, nil for none).
type ImageInfo struct {
	Name     string
	Type     wire.Kind
	SHA256   string
	Built    bool
	Keep     int
	Schedule json.RawMessage
}

// Images returns the names of all Images, sorted.
func (s *Set) Images() []string { return sortedKeys(s.images) }

// ImageInfo returns the facts of one Image; ok is false when it does not
// exist.
func (s *Set) ImageInfo(name string) (ImageInfo, bool) {
	img, ok := s.images[name]
	if !ok {
		return ImageInfo{}, false
	}
	info := ImageInfo{Name: name, Type: wire.Kind(img.Type), SHA256: img.SHA256, Built: img.Build != nil, Keep: DefaultKeep}
	if img.Retention != nil && img.Retention.Keep > 0 {
		info.Keep = img.Retention.Keep
	}
	if len(img.Schedule) > 0 && string(img.Schedule) != "null" {
		var v any
		if decode(img.Schedule, &v) == nil {
			info.Schedule, _ = json.Marshal(v) // sorted keys: one form for one value
		}
	}
	return info, true
}
