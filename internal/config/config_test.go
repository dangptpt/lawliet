package config

import (
	"errors"
	"flag"
	"io"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	c, err := Parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr() != "127.0.0.1:6380" {
		t.Fatalf("Addr() = %q", c.Addr())
	}
}

func TestParseFlags(t *testing.T) {
	c, err := Parse([]string{"--bind", "0.0.0.0", "--port", "7000"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr() != "0.0.0.0:7000" {
		t.Fatalf("Addr() = %q", c.Addr())
	}
}

func TestParseErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--port", "70000"},
		{"--port", "abc"},
		{"extra"},
	} {
		if _, err := Parse(args, io.Discard); err == nil {
			t.Errorf("Parse(%q): expected error", args)
		}
	}
	if _, err := Parse([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: want flag.ErrHelp, got %v", err)
	}
}
