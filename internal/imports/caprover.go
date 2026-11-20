package imports

import (
	"encoding/json"
	"fmt"

	"github.com/behzodhalil/yawl/internal/compose"
	"github.com/behzodhalil/yawl/internal/spec"
)

func CapRover(file []byte, name string) (compose.Result, error) {
	var c struct {
		DockerfilePath  string   `json:"dockerfilePath"`
		DockerfileLines []string `json:"dockerfileLines"`
		ImageName       string   `json:"imageName"`
		TemplateID      string   `json:"templateId"`
	}
	if err := json.Unmarshal(file, &c); err != nil {
		return compose.Result{}, fmt.Errorf("not a captain-definition: %w", err)
	}
	var r compose.Result
	s := spec.Service{Name: name, Kind: spec.KindWeb, Port: 80, Image: c.ImageName} // 80 is CapRover's default
	switch {
	case c.ImageName != "":
	case len(c.DockerfileLines) > 0:
		warn(&r, name, "dockerfileLines are not carried; Yawl builds the Dockerfile at the repository's root, so move them there.")
	case c.TemplateID != "":
		warn(&r, name, "templateId %s is not carried; write a Dockerfile at the repository's root.", c.TemplateID)
	case c.DockerfilePath != "" && c.DockerfilePath != "./Dockerfile" && c.DockerfilePath != "Dockerfile":
		warn(&r, name, "builds %s; Yawl builds the Dockerfile at the repository's root, so move it there.", c.DockerfilePath)
	}
	r.Services = append(r.Services, s)
	warn(&r, name, "variables, persistent directories and the port are kept in CapRover, not in the file; set the variables with yawl env set, add volumes to yawl.yaml, and change port if the app listens on another.")
	return r, nil
}
