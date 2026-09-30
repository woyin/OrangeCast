package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Both renderers accept only headings, lists and text. Untrusted links stay text.
func TestSafeRenderingClientServerParity(t *testing.T) {
	path, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	cases := []string{"# 中文\n- 第一项\n- second\n\n正文", "<script>alert(1)</script>\n<img src=x onerror=alert(1)>", "[click](javascript:alert(1))\n</p><svg onload=alert(1)>", "### 标题 & \"引语\"\nO'Reilly\n**literal**"}
	input, _ := json.Marshal(cases)
	source, err := os.ReadFile("static/safe-render.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source) + "\nprocess.stdout.write(JSON.stringify(" + string(input) + ".map(CWPSafe.markdown)));"
	file := filepath.Join(t.TempDir(), "safe.js")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(path, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
	var rendered []string
	if err := json.Unmarshal(out, &rendered); err != nil {
		t.Fatal(err)
	}
	for i, text := range cases {
		if rendered[i] != wechatRichText(text) {
			t.Fatalf("case %d: client=%q server=%q", i, rendered[i], wechatRichText(text))
		}
	}
}
