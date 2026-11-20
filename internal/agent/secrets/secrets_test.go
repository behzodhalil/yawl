package secrets

import (
	"strings"
	"testing"
)

func TestMaskLongestFirst(t *testing.T) {
	m := &Masker{}
	m.Add("abcdef", "abcdefgh")
	if got := m.Mask("x abcdefgh y abcdef"); got != "x •••• y ••••" {
		t.Fatalf("%q", got)
	}
}

func TestMaskIgnoresShort(t *testing.T) {
	m := &Masker{}
	m.Add("abc")
	if got := m.Mask("abc abc"); got != "abc abc" {
		t.Fatalf("%q", got)
	}
}

func TestKeyringCreatesAndReloads(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil || a.Public != b.Public {
		t.Fatalf("second load made a new key (%v)", err)
	}
}

func TestMaskMultiLineValuePerLine(t *testing.T) {
	m := &Masker{}
	m.Add("-----BEGIN KEY-----\nMIIEvQIBADANBgkqhkiG9w0\nab\n-----END KEY-----")
	if got := m.Mask("bad key: MIIEvQIBADANBgkqhkiG9w0"); got != "bad key: ••••" {
		t.Fatalf("%q", got)
	}
	if got := m.Mask("ab"); got != "ab" {
		t.Fatal("short lines are not masked")
	}
}

func TestMaskEscapedForms(t *testing.T) {
	m := &Masker{}
	m.Add(`p&ss "w/rd" <x>`)
	for _, in := range []string{
		`{"pw":"p&ss \"w/rd\" <x>"}`,
		`{"pw":"p&ss \"w/rd\" <x>"}`,
		"?pw=p%26ss+%22w%2Frd%22+%3Cx%3E",
		"/p&ss%20%22w%2Frd%22%20%3Cx%3E",
	} {
		if got := m.Mask(in); strings.Contains(got, "ss") {
			t.Fatalf("%s -> %s", in, got)
		}
	}
}

func TestMaskOverlap(t *testing.T) {
	m := &Masker{}
	m.Add("abcdefXYZ", "XYZ123456")
	if got := m.Mask("[abcdefXYZ123456]"); got != "[••••]" {
		t.Fatalf("%q", got)
	}
}
