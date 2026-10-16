package config

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCN35EnvPrecedenceAndEmptyValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.env")
	contents := "MERCD_ADDR=127.0.0.1:19001\nMERCD_SERVER_HOST=127.0.0.1:19001\nMERCD_CARDS='sample cards'\nMERCD_REQUEST_LOG=file.log\nMERCD_VERBOSE=true\nMERCD_REMOTE_ASSETS_SCAN=12s\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MERCD_ENV_FILE", path)
	t.Setenv("MERCD_ADDR", "127.0.0.1:19002")
	t.Setenv("MERCD_SERVER_HOST", "127.0.0.1:19002")
	t.Setenv("MERCD_REQUEST_LOG", "")
	var c CN35Config
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c.BindCN35(fs)
	if err := fs.Parse([]string{"-addr", "127.0.0.1:19003", "-v=false"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyCN35Env(fs); err != nil {
		t.Fatal(err)
	}
	if c.Addr != "127.0.0.1:19003" || c.ServerHost != "127.0.0.1:19002" {
		t.Fatalf("flag/env precedence failed: addr=%q host=%q", c.Addr, c.ServerHost)
	}
	if c.CardsDir != "sample cards" || c.RemoteAssetsScan != 12*time.Second {
		t.Fatal("file values not applied")
	}
	if c.Verbose || c.RequestLog != "" {
		t.Fatal("explicit false/empty values were lost")
	}
	if c.StateDir != DefaultCN35StateDir {
		t.Fatal("unconfigured default changed")
	}
}

func TestCN35EnvRejectsInvalidAndMissingFile(t *testing.T) {
	for _, tc := range []struct {
		name, contents string
		missing        bool
	}{
		{"invalid-duration", "MERCD_REMOTE_ASSETS_SCAN=invalid\n", false},
		{"invalid-bool", "MERCD_VERBOSE=invalid\n", false},
		{"missing-file", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.env")
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var c CN35Config
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			c.BindCN35(fs)
			if err := fs.Parse([]string{"-env-file", path}); err != nil {
				t.Fatal(err)
			}
			if err := c.ApplyCN35Env(fs); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestCN35EnvDoesNotEvaluateShellOrExposeInvalidLine(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	path := filepath.Join(dir, "settings.env")
	literal := "$(touch " + marker + ")"
	if err := os.WriteFile(path, []byte("MERCD_CARDS="+literal+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var c CN35Config
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	c.BindCN35(fs)
	if err := fs.Parse([]string{"-env-file", path}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyCN35Env(fs); err != nil {
		t.Fatal(err)
	}
	if c.CardsDir != literal {
		t.Fatal("file value was expanded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("shell content executed")
	}
	if err := os.WriteFile(path, []byte("PRIVATE-VALUE-WITHOUT-EQUALS"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadEnvFile(path)
	if err == nil || strings.Contains(err.Error(), "PRIVATE-VALUE") {
		t.Fatal("invalid line leaked")
	}
}
