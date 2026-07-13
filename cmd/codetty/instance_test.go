package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestExtractInstanceArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantRest []string
		wantInst string
		wantErr  bool
	}{
		{"no flag", []string{"ls"}, []string{"ls"}, "", false},
		{"empty args", []string{}, []string{}, "", false},
		{"short flag", []string{"-i", "work", "ls"}, []string{"ls"}, "work", false},
		{"long flag", []string{"--instance", "work", "attach"}, []string{"attach"}, "work", false},
		{"long equals", []string{"--instance=work"}, []string{}, "work", false},
		{"short equals", []string{"-i=work", "ls"}, []string{"ls"}, "work", false},
		{"missing value", []string{"-i"}, nil, "", true},
		{"only before subcommand", []string{"bash", "-i"}, []string{"bash", "-i"}, "", false},
		{"not consumed after subcommand", []string{"new", "-i", "x"}, []string{"new", "-i", "x"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest, inst, err := extractInstanceArgs(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if inst != tc.wantInst {
				t.Errorf("instance = %q, want %q", inst, tc.wantInst)
			}
			if !reflect.DeepEqual(rest, tc.wantRest) {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

func TestValidateInstanceName(t *testing.T) {
	valid := []string{"", "work", "my-project", "a_b", "X9"}
	for _, n := range valid {
		if err := validateInstanceName(n); err != nil {
			t.Errorf("validateInstanceName(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{"a b", "a/b", "../x", "名字", "a.b", string(make([]byte, 65))}
	for _, n := range invalid {
		if err := validateInstanceName(n); err == nil {
			t.Errorf("validateInstanceName(%q) = nil, want error", n)
		}
	}
}

func TestInstanceSocketPath(t *testing.T) {
	dir := runtimeSocketDir()
	if got, want := instanceSocketPath(""), filepath.Join(dir, "codetty.sock"); got != want {
		t.Errorf("default instance socket = %q, want %q", got, want)
	}
	if got, want := instanceSocketPath("work"), filepath.Join(dir, "codetty-work.sock"); got != want {
		t.Errorf("named instance socket = %q, want %q", got, want)
	}
}
