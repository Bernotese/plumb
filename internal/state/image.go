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

// Recipe is what a build of an Image makes (T-039 Plan §1). Two revisions
// with the same Recipe hash build the same template, so a build Plan is
// stale only when the hash at the head differs (D-199). The settings of
// the build guest other than the disk (cores, memory, bridge, ip) are not
// in it: they do not change what is inside the template.
type Recipe struct {
	Type      wire.Kind       `json:"type"`
	URL       string          `json:"url"`
	SHA256    string          `json:"sha256"`
	Upgrade   bool            `json:"upgrade"`
	DiskGB    int             `json:"disk"`
	Resources []wire.Resource `json:"resources"`
}

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
			Resources: r.Resources,
		},
	}
	out.RecipeSHA256 = out.Recipe.Hash()
	return out, nil
}
