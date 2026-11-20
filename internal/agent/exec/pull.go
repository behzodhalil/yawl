package exec

import (
	"fmt"
	"strings"

	"github.com/behzodhalil/yawl/internal/spec"
)

func pullCause(ref string, err error, arch string) string {
	msg := strings.ToLower(err.Error())
	host := spec.RegistryHost(ref)
	switch {
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "denied") || strings.Contains(msg, "authentication required"):
		return fmt.Sprintf("%s refused the pull: add a login for %s in Integrations, Registries.", host, host)
	case strings.Contains(msg, "no matching manifest for linux/"):
		return fmt.Sprintf("%s has no linux/%s image; this server is %s.", ref, arch, arch)
	case strings.Contains(msg, "manifest unknown") || strings.Contains(msg, "not found"):
		return ref + " does not exist."
	}
	return fmt.Sprintf("pull %s: %v", ref, err)
}

func pushCause(ref string, err error) string {
	msg := strings.ToLower(err.Error())
	host := spec.RegistryHost(ref)
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "denied") || strings.Contains(msg, "authentication required") {
		return fmt.Sprintf("%s refused the push: its login in Integrations, Registries must be allowed to write %s.", host, spec.ImageName(ref))
	}
	return fmt.Sprintf("push %s: %v", ref, err)
}
