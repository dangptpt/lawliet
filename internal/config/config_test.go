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

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"0": 0, "1048576": 1048576, "100b": 100,
		"1k": 1000, "1kb": 1024, "512mb": 512 << 20, "2gb": 2 << 30, "2G": 2e9, " 3m ": 3e6,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-1", "1tb", "1.5gb", "99999999999gb"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should fail", bad)
		}
	}
}

func TestParseMemoryFlags(t *testing.T) {
	c, err := Parse([]string{"--maxmemory", "2gb", "--maxmemory-policy", "allkeys-lru", "--max-collections", "5"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxMemory != 2<<30 || c.MaxMemoryPol != "allkeys-lru" || c.MaxCollections != 5 {
		t.Fatalf("%+v", c)
	}
	if _, err := Parse([]string{"--maxmemory", "lots"}, io.Discard); err == nil {
		t.Error("bad --maxmemory should fail")
	}
}
