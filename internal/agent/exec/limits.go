package exec

import "fmt"

// checkCPU refuses a CPU limit above the host's cores with a sentence
// (design record 23, L5); Docker's own answer is a range error.
func checkCPU(service string, cpu float64, host int) string {
	if cpu > float64(host) {
		return fmt.Sprintf("%s asks for %g CPUs; this server has %d.", service, cpu, host)
	}
	return ""
}
