package egress

import "testing"

func TestPublic(t *testing.T) {
	for addr, ok := range map[string]bool{
		"1.1.1.1:443": true, "[2606:4700::1111]:443": true,
		"127.0.0.1:7000": false, "[::1]:80": false, "0.0.0.0:80": false,
		"169.254.169.254:80": false, "[fe80::1]:80": false,
		"10.0.0.1:443": false, "172.17.0.1:2019": false, "192.168.1.1:80": false, "[fd00::1]:80": false,
		"100.100.100.100:53": false, "[::ffff:127.0.0.1]:80": false, "224.0.0.1:80": false,
	} {
		if err := Public("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("%s: %v", addr, err)
		}
	}
}

func TestPublicRefusesHostOwnAddress(t *testing.T) {
	hosts, err := HostAddrs(" 198.51.100.7, 2001:db8:1::7 ,::ffff:198.51.100.9")
	if err != nil {
		t.Fatal(err)
	}
	Host = hosts
	defer func() { Host = nil }()
	for addr, ok := range map[string]bool{"198.51.100.7:22": false, "[::ffff:198.51.100.7]:22": false, "[2001:db8:1::7]:5432": false,
		"198.51.100.9:80": false, "198.51.100.7:8443": false, "198.51.100.9:443": true, "[::ffff:198.51.100.7]:443": true,
		"[2001:db8:1::7]:443": true, "198.51.100.8:22": true, "1.1.1.1:443": true} {
		if err := Public("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("%s: %v", addr, err)
		}
	}
	if _, err := HostAddrs("198.51.100.7, nope"); err == nil {
		t.Error("a bad address was taken")
	}
}
