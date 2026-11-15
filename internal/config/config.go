// Package config parses server command-line flags.
package config

import (
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
)

// Config holds server settings. More fields are added per milestone (see DESIGN.md §12).
type Config struct {
	Bind        string
	Port        int
	ShowVersion bool
}

// Addr returns the listen address "bind:port".
func (c Config) Addr() string { return net.JoinHostPort(c.Bind, strconv.Itoa(c.Port)) }

// Parse parses args (without the program name). Returns flag.ErrHelp for -h/--help.
func Parse(args []string, out io.Writer) (Config, error) {
	var c Config
	fs := flag.NewFlagSet("lawliet", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&c.Bind, "bind", "127.0.0.1", "interface to listen on")
	fs.IntVar(&c.Port, "port", 6380, "TCP port")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument: %q", fs.Arg(0))
	}
	if c.Port < 0 || c.Port > 65535 {
		return Config{}, fmt.Errorf("invalid --port %d: must be 0-65535", c.Port)
	}
	return c, nil
}
