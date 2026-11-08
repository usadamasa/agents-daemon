import { test } from "node:test";
import assert from "node:assert/strict";
import { segments } from "../src/links.js";

const ctx = { githubRepo: "o/r", base: "/repo/x" };
const links = (text, c = ctx) => segments(text, c).filter((s) => s.target !== undefined);

test("segments: 切り分けた text をつなぐと元の文字列に戻る", () => {
  const text = "見る https://a.example/b。#12 と x/y#3、gui/main.go:5 と /a/b/c.go。";
  assert.equal(segments(text, ctx).map((s) => s.text).join(""), text);
});

test("segments: リンクの無い文字列は 1 つの text のまま", () => {
  assert.deepEqual(segments("ただの文字列", ctx), [{ text: "ただの文字列" }]);
});

test("segments: URL は http と https だけで、ASCII の外と文末の記号を含めない", () => {
  assert.deepEqual(links("https://a.example/b?c=1#d を見る"), [{ text: "https://a.example/b?c=1#d", target: "https://a.example/b?c=1#d" }]);
  assert.equal(links("https://a.example/bを見る")[0].target, "https://a.example/b");
  assert.equal(links("http://a.example/b.")[0].target, "http://a.example/b");
  assert.equal(links("(https://a.example/b)")[0].target, "https://a.example/b");
  assert.equal(links("https://a.example/w_(x)")[0].target, "https://a.example/w_(x)");
  assert.deepEqual(links("ftp://a.example/b javascript:alert(1)"), []);
});

test("segments: owner/repo#123 は GitHub のページ。pr: は pull、issue: と接頭辞なしは issues", () => {
  assert.equal(links("a/b.c#12 を見る")[0].target, "https://github.com/a/b.c/issues/12");
  assert.equal(links("issue:a/b#12")[0].target, "https://github.com/a/b/issues/12");
  const pr = links("pr:a/b#12 を待つ")[0];
  assert.equal(pr.text, "pr:a/b#12");
  assert.equal(pr.target, "https://github.com/a/b/pull/12");
});

test("segments: #123 は repo の owner/repo を補う。日本語に挟まれても拾う", () => {
  assert.equal(links("#12")[0].target, "https://github.com/o/r/issues/12");
  assert.equal(links("これは#12を直す")[0].text, "#12");
  assert.equal(links("(#12)")[0].text, "#12");
});

test("segments: GitHub の repo が分からなければ #123 は素の文字", () => {
  assert.deepEqual(links("#12", { base: "/repo/x" }), []);
});

test("segments: # を拾いすぎない", () => {
  for (const text of ["C#1", "a#12", "&#123;", "# 12", "#0", "#12a", "##12"]) {
    assert.deepEqual(links(text), [], text);
  }
});

test("segments: 絶対パスは 2 階層以上。行番号は text に残し target から外す", () => {
  assert.deepEqual(links("/a/b/c.go:12 を見る"), [{ text: "/a/b/c.go:12", target: "/a/b/c.go" }]);
  assert.equal(links("/a/b.")[0].target, "/a/b");
  assert.equal(links("/a/b/")[0].target, "/a/b/");
});

test("segments: 相対パスは base に足す。拡張子か行番号が要る", () => {
  assert.deepEqual(links("gui/main.go:12"), [{ text: "gui/main.go:12", target: "/repo/x/gui/main.go" }]);
  assert.equal(links(".claude/skills/a.md")[0].target, "/repo/x/.claude/skills/a.md");
  assert.equal(links("path/to/file:12")[0].target, "/repo/x/path/to/file");
  assert.equal(links("gui/main.go。")[0].target, "/repo/x/gui/main.go");
});

test("segments: base が無ければ相対パスは素の文字で、絶対パスはリンクのまま", () => {
  assert.deepEqual(links("gui/main.go", {}), []);
  assert.equal(links("/a/b", {})[0].target, "/a/b");
});

test("segments: / を拾いすぎない", () => {
  for (const text of ["and/or", "TCP/IP", "owner/repo", "path/to/file", "2026/11/08", "1.2/3.4", "/deps", "/agents-daemon:setup", "example.com/a.html", "git@github.com:o/r.git", "file:///a"]) {
    assert.deepEqual(links(text), [], text);
  }
});

test("segments: URL の中のパスや # を別のリンクにしない", () => {
  const got = links("https://a.example/x/y.go#12");
  assert.equal(got.length, 1);
  assert.equal(got[0].target, "https://a.example/x/y.go#12");
});
