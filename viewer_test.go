package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestViewerSVGURLLifetime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	start := strings.Index(viewerHTML, "async function unpack(")
	end := strings.Index(viewerHTML, "// Draw one side")
	if start < 0 || end <= start {
		t.Fatal("SVG loading functions not found")
	}
	script := `const assert = require("node:assert/strict");
const zlib = require("node:zlib");
const active = new Set();
const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL);
URL.createObjectURL = blob => { const url = create(blob); active.add(url); return url; };
URL.revokeObjectURL = url => { assert(active.delete(url), "URL revoked twice"); revoke(url); };
let fail = false;
global.Image = class {
 async decode() {
  if (fail) throw new Error("decode failed");
  assert.equal(await (await fetch(this.src)).text(), "<svg/>");
 }
};
` + viewerHTML[start:end] + `
(async () => {
 const packed = zlib.gzipSync("<svg/>").toString("base64");
 for (let i = 0; i < 20; i++) {
  assert(await unpack(packed));
  assert.equal(active.size, 0, "successful decoding leaked a URL");
 }
 fail = true;
 assert.equal(await unpack(packed), null);
 assert.equal(active.size, 0, "failed decoding leaked a URL");
 fail = false;
 // A corrupt side of a page must not leave the other side's URL unclaimed.
 await Promise.allSettled([unpack(packed), unpack("AAAA")]);
 assert.equal(active.size, 0, "a failed sibling load leaked a URL");
 assert.equal(await unpack(null), null);
})().catch(error => { console.error(error); process.exitCode = 1; });`
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("SVG URL lifecycle: %v\n%s", err, out)
	}
}
