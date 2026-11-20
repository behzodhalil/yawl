package imports

import (
	"testing"

	"github.com/behzodhalil/yawl/internal/spec"
)

func TestCapRover(t *testing.T) {
	const kept = "shop: variables, persistent directories and the port are kept in CapRover, not in the file; set the variables with yawl env set, add volumes to yawl.yaml, and change port if the app listens on another."
	for _, c := range []struct {
		file, image, note string
	}{
		{`{"schemaVersion": 2, "dockerfilePath": "./Dockerfile"}`, "", ""},
		{`{"schemaVersion": 2, "dockerfilePath": "./deploy/Dockerfile"}`, "", "shop: builds ./deploy/Dockerfile; Yawl builds the Dockerfile at the repository's root, so move it there."},
		{`{"schemaVersion": 2, "dockerfileLines": ["FROM node:22-alpine", "COPY . .", "CMD [\"node\", \"server.js\"]"]}`, "", "shop: dockerfileLines are not carried; Yawl builds the Dockerfile at the repository's root, so move them there."},
		{`{"schemaVersion": 2, "imageName": "nginx:1.27"}`, "nginx:1.27", ""},
	} {
		r, err := CapRover([]byte(c.file), "shop")
		if err != nil {
			t.Fatal(err)
		}
		if s := r.Services[0]; len(r.Services) != 1 || s.Name != "shop" || s.Kind != spec.KindWeb || s.Port != 80 || s.Image != c.image {
			t.Fatalf("%s: %+v", c.file, r.Services)
		}
		hasNotes(t, r, kept)
		if c.note != "" {
			hasNotes(t, r, c.note)
		} else if len(r.Warnings) != 1 {
			t.Errorf("%s: notes %q", c.file, r.Warnings)
		}
	}
	if _, err := CapRover([]byte("FROM node"), "shop"); err == nil {
		t.Fatal("not JSON was read")
	}
}
