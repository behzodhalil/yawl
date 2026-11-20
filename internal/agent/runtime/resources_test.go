package runtime

import "testing"

func TestResources(t *testing.T) {
	r := resources(ContainerSpec{CPU: 0.5, Memory: 512e6})
	if r.NanoCPUs != 5e8 || r.Memory != 512e6 || r.MemorySwap != 512e6 {
		t.Fatalf("%+v", r)
	}
	if r := resources(ContainerSpec{}); r.NanoCPUs != 0 || r.Memory != 0 || r.MemorySwap != 0 {
		t.Fatalf("no limits: %+v", r)
	}
}
