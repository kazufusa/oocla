package main

import "testing"

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	if err := run([]string{"nope"}); err == nil {
		t.Fatal("want error for unknown subcommand, got nil")
	}
}

func TestRunRequiresSubcommand(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("want error when no subcommand given, got nil")
	}
}

func TestRunVersion(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}
}
