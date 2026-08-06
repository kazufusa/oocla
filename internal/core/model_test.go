package core

import "testing"

func TestLookupKnownAliases(t *testing.T) {
	r := NewRegistry(testTime())
	for _, in := range []string{"opus", "opus:latest", "OPUS", "opus:LATEST"} {
		m, ok := r.Lookup(in)
		if !ok {
			t.Fatalf("Lookup(%q): not found", in)
		}
		if m.CLIName != "opus" {
			t.Errorf("Lookup(%q).CLIName = %q, want %q", in, m.CLIName, "opus")
		}
		if m.Name != "opus:latest" {
			t.Errorf("Lookup(%q).Name = %q, want %q", in, m.Name, "opus:latest")
		}
	}
}

func TestLookupFullModelIDPassthrough(t *testing.T) {
	r := NewRegistry(testTime())
	m, ok := r.Lookup("claude-haiku-4-5-20251001")
	if !ok {
		t.Fatal("full model id should pass through")
	}
	if m.CLIName != "claude-haiku-4-5-20251001" {
		t.Errorf("CLIName = %q", m.CLIName)
	}
	if m.Name != "claude-haiku-4-5-20251001:latest" {
		t.Errorf("Name = %q", m.Name)
	}
}

func TestLookupUnknown(t *testing.T) {
	r := NewRegistry(testTime())
	for _, in := range []string{"", "llama3", "gpt-4o", "opus:v2"} {
		if _, ok := r.Lookup(in); ok {
			t.Errorf("Lookup(%q): want not found", in)
		}
	}
}

func TestListIsStableAndTagged(t *testing.T) {
	r := NewRegistry(testTime())
	got := r.List()
	if len(got) == 0 {
		t.Fatal("List() is empty")
	}
	seen := map[string]bool{}
	for _, m := range got {
		if seen[m.Name] {
			t.Errorf("duplicate model %q", m.Name)
		}
		seen[m.Name] = true
		if len(m.Digest) != 64 {
			t.Errorf("%s: digest %q is not 64 hex chars", m.Name, m.Digest)
		}
		if m.Size <= 0 {
			t.Errorf("%s: size = %d, want > 0", m.Name, m.Size)
		}
	}
	for _, want := range []string{"opus:latest", "sonnet:latest", "haiku:latest"} {
		if !seen[want] {
			t.Errorf("List() is missing %q", want)
		}
	}
	// The registry must not reorder between calls: clients diff this list.
	second := r.List()
	for i := range got {
		if got[i].Name != second[i].Name {
			t.Fatalf("List() is not stable at %d: %q vs %q", i, got[i].Name, second[i].Name)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5":              "5",
		"claude-sonnet-5":            "5",
		"claude-haiku-4-5-20251001":  "4.5",
		"claude-opus-4-1-20250805":   "4.1",
		"claude-3-5-sonnet-20241022": "3.5",
		"claude-opus":                "",
		"":                           "",
	}
	for id, want := range cases {
		if got := parseVersion(id); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestSetResolvedVersionsTheCatalog(t *testing.T) {
	r := NewRegistry(testTime())
	r.SetResolved("opus", "claude-opus-5")

	m, ok := r.Lookup("opus")
	if !ok {
		t.Fatal("opus vanished after SetResolved")
	}
	if m.Name != "opus:5" || m.Version != "5" || m.ResolvedID != "claude-opus-5" {
		t.Errorf("resolved model = %+v", m)
	}
	// The version becomes an accepted tag; latest and the bare alias survive.
	for _, in := range []string{"opus:5", "opus:latest", "opus", "OPUS:5"} {
		if _, ok := r.Lookup(in); !ok {
			t.Errorf("Lookup(%q): not found", in)
		}
	}
	if _, ok := r.Lookup("opus:4"); ok {
		t.Error("a wrong version tag must not resolve")
	}
	// The list advertises the versioned name, in place.
	if got := r.List(); got[0].Name != "opus:5" {
		t.Errorf("List()[0].Name = %q, want %q", got[0].Name, "opus:5")
	}
	// Other entries are untouched.
	if m, _ := r.Lookup("sonnet"); m.Name != "sonnet:latest" {
		t.Errorf("sonnet = %q, want still latest", m.Name)
	}
}

func TestSetResolvedIgnoresTheUnusable(t *testing.T) {
	r := NewRegistry(testTime())
	r.SetResolved("opus", "weird-id-with-no-version")
	if m, _ := r.Lookup("opus"); m.Name != "opus:latest" {
		t.Errorf("an unusable id changed the catalog: %q", m.Name)
	}
	r.SetResolved("llama3", "claude-opus-5")
	if _, ok := r.Lookup("llama3"); ok {
		t.Error("SetResolved invented a model")
	}
}

func TestLookupPassthroughCarriesVersion(t *testing.T) {
	r := NewRegistry(testTime())
	m, _ := r.Lookup("claude-haiku-4-5-20251001")
	if m.Version != "4.5" || m.ResolvedID != "claude-haiku-4-5-20251001" {
		t.Errorf("passthrough version = %q, resolved = %q", m.Version, m.ResolvedID)
	}
}

func TestDigestIsDeterministicPerModel(t *testing.T) {
	a, _ := NewRegistry(testTime()).Lookup("opus")
	b, _ := NewRegistry(testTime()).Lookup("opus:latest")
	if a.Digest != b.Digest {
		t.Errorf("digest differs between lookups: %q vs %q", a.Digest, b.Digest)
	}
	c, _ := NewRegistry(testTime()).Lookup("haiku")
	if a.Digest == c.Digest {
		t.Error("different models share a digest")
	}
}

func TestCapabilities(t *testing.T) {
	m, _ := NewRegistry(testTime()).Lookup("opus")
	want := map[string]bool{"completion": true, "tools": true}
	for _, c := range m.Capabilities {
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("missing capabilities %v in %v", want, m.Capabilities)
	}
}
