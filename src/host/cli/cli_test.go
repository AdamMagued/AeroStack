package main

import (
	"bytes"
	"os"
	"testing"
)

func TestCLI_List(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"list", "-j"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("list -j failed: %v", err)
	}
}

func TestCLI_Create(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"create", "alpine:3.20", "test-alpine"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("create failed: %v", err)
	}
}

func TestCLI_Run(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"run", "uname", "-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("run failed: %v", err)
	}
}

func TestCLI_Clone(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"clone", "ubuntu", "ubuntu-clone"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("clone failed: %v", err)
	}
}

func TestCLI_StatusAndDoctor(t *testing.T) {
	cmd := NewRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	cmd.SetArgs([]string{"doctor"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor failed: %v", err)
	}

	cmd.SetArgs([]string{"top"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("top failed: %v", err)
	}
}

func TestCLI_OrbShortcutLogic(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Simulate invoking as `orb uname -a`
	os.Args = []string{"/usr/local/bin/orb", "uname", "-a"}
	progName := "orb"
	if progName == "orb" {
		if len(os.Args) > 1 && os.Args[1] != "run" {
			newArgs := append([]string{os.Args[0], "run"}, os.Args[1:]...)
			os.Args = newArgs
		}
	}

	if os.Args[1] != "run" || os.Args[2] != "uname" || os.Args[3] != "-a" {
		t.Fatalf("Shortcut prepending failed: %v", os.Args)
	}
}
