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

func TestParseBackupArgs(t *testing.T) {
	if dest, err := parseBackupArgs([]string{"out.tar.gz"}); err != nil || dest != "out.tar.gz" {
		t.Errorf("parseBackupArgs([out.tar.gz]) = %q, %v", dest, err)
	}
	if _, err := parseBackupArgs(nil); err == nil {
		t.Error("无参数应报错")
	}
	if _, err := parseBackupArgs([]string{"-bad-flag"}); err == nil {
		t.Error("非法 flag 应报错")
	}
}

func TestParseRestoreArgs(t *testing.T) {
	src, force, err := parseRestoreArgs([]string{"in.tar.gz"})
	if err != nil || src != "in.tar.gz" || force {
		t.Errorf("parseRestoreArgs([in.tar.gz]) = %q, %v, %v", src, force, err)
	}
	src, force, err = parseRestoreArgs([]string{"--force", "in.tar.gz"})
	if err != nil || src != "in.tar.gz" || !force {
		t.Errorf("带 --force 应解析为 force=true，实际 %q, %v, %v", src, force, err)
	}
	if _, _, err := parseRestoreArgs(nil); err == nil {
		t.Error("无参数应报错")
	}
	if _, _, err := parseRestoreArgs([]string{"-bad"}); err == nil {
		t.Error("非法 flag 应报错")
	}
}
