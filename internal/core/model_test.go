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
