package pow

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFrontendSolverMatchesVectors 用同一组 Python 向量校验 web/static/js/pow.js，
// 保证浏览器算出的 nonce 和服务端认可的完全一致。没装 node 时跳过。
func TestFrontendSolverMatchesVectors(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := filepath.Join("..", "..", "..", "web", "static", "js", "pow.js")
	output, err := exec.Command(node, filepath.Join("testdata", "check_pow_js.js"), script, filepath.Join("testdata", "vectors.json")).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "ok" {
		t.Fatalf("pow.js vectors: %v\n%s", err, output)
	}
}
