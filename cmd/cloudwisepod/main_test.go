package main

import (
	"reflect"
	"testing"
)

// TestParseCommand 验证 CLI 命令分发解析。
func TestParseCommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		cmd  string
		rest []string
	}{
		{"no args", nil, "", nil},
		{"serve default", []string{"serve"}, "serve", []string{}},
		{"backup", []string{"backup", "dest.tar.gz"}, "backup", []string{"dest.tar.gz"}},
		{"restore", []string{"restore", "src.tar.gz", "--force"}, "restore", []string{"src.tar.gz", "--force"}},
		{"unknown", []string{"bogus"}, "bogus", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rest := parseCommand(c.args)
			if got != c.cmd {
				t.Errorf("parseCommand(%v) cmd = %q, want %q", c.args, got, c.cmd)
			}
			if !reflect.DeepEqual(rest, c.rest) {
				t.Errorf("parseCommand(%v) rest = %v, want %v", c.args, rest, c.rest)
			}
		})
	}
}

// TestEnsureArchiveExt 验证备份目标路径扩展名补全。
func TestEnsureArchiveExt(t *testing.T) {
	if got := ensureArchiveExt("backup"); got != "backup.tar.gz" {
		t.Errorf("ensureArchiveExt(backup)=%q want backup.tar.gz", got)
	}
	if got := ensureArchiveExt("backup.tar.gz"); got != "backup.tar.gz" {
		t.Errorf("已带扩展名应原样，实际 %q", got)
	}
	if got := ensureArchiveExt("/path/to/x"); got != "/path/to/x.tar.gz" {
		t.Errorf("绝对路径无扩展应补全，实际 %q", got)
	}
}
