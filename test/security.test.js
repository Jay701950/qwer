import test from "node:test";
import assert from "node:assert/strict";
import { isBlockedAddress, rewriteHtml } from "../src/server.js";

test("blocks common private and metadata addresses", () => {
  for (const address of ["127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "::1", "fc00::1", "fe80::1"]) {
    assert.equal(isBlockedAddress(address), true, address);
  }
});

test("allows public addresses", () => {
  assert.equal(isBlockedAddress("93.184.216.34"), false);
  assert.equal(isBlockedAddress("2001:4860:4860::8888"), false);
});

test("rewrites common HTML references to the proxy", () => {
  const output = rewriteHtml('<a href="/next">Next</a><img src="/img.png"><form action="/search"><link href="https://cdn.example/style.css">', new URL("https://example.com/page"));
  assert.match(output, /view\?url=https%3A%2F%2Fexample.com%2Fnext/);
  assert.match(output, /view\?url=https%3A%2F%2Fexample.com%2Fimg.png/);
  assert.match(output, /view\?url=https%3A%2F%2Fcdn.example%2Fstyle.css/);
});
