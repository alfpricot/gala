"use strict";

// npm/platforms.json drives the launcher and npm/build.sh; these checks keep
// the per-platform manifests and the wrapper's optionalDependencies in step
// with it, so a platform added in one place and not the others fails here
// instead of at release time.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const NPM_ROOT = path.join(__dirname, "..");
const PLATFORMS = require(path.join(NPM_ROOT, "platforms.json"));
const { resolvePlatformPackage } = require(path.join(NPM_ROOT, "bin", "gala.js"));

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function withProcessOverride(platform, arch, fn) {
  const original = { platform: process.platform, arch: process.arch };
  Object.defineProperty(process, "platform", { value: platform, configurable: true });
  Object.defineProperty(process, "arch", { value: arch, configurable: true });
  try {
    fn();
  } finally {
    Object.defineProperty(process, "platform", { value: original.platform, configurable: true });
    Object.defineProperty(process, "arch", { value: original.arch, configurable: true });
  }
}

test("every platforms.json entry resolves through resolvePlatformPackage", () => {
  for (const [key, entry] of Object.entries(PLATFORMS)) {
    withProcessOverride(entry.platform, entry.arch, () => {
      const resolved = resolvePlatformPackage();
      assert.equal(resolved.pkg, entry.package, `${key}: package name mismatch`);
      assert.equal(resolved.binName, entry.binName, `${key}: binary name mismatch`);
    });
  }
});

test("an unsupported platform is a clear error", () => {
  withProcessOverride("aix", "ppc64", () => {
    assert.throws(resolvePlatformPackage, /no prebuilt binary for aix\/ppc64/);
  });
});

test("every platforms.json entry has a matching npm/packages/<key> manifest", () => {
  const wrapper = readJson(path.join(NPM_ROOT, "package.json"));
  for (const [key, entry] of Object.entries(PLATFORMS)) {
    const manifest = readJson(path.join(NPM_ROOT, "packages", key, "package.json"));
    assert.equal(manifest.name, entry.package, `${key}: name mismatch`);
    assert.equal(manifest.version, wrapper.version, `${key}: version mismatch`);
    assert.deepEqual(manifest.os, [entry.platform], `${key}: os mismatch`);
    assert.deepEqual(manifest.cpu, [entry.arch], `${key}: cpu mismatch`);
    assert.deepEqual(manifest.files, [`bin/${entry.binName}`], `${key}: files mismatch`);
    assert.deepEqual(manifest.publishConfig, wrapper.publishConfig, `${key}: registry mismatch`);
  }
});

test("the wrapper declares an optionalDependency for every platforms.json entry", () => {
  const wrapper = readJson(path.join(NPM_ROOT, "package.json"));
  const declared = Object.keys(wrapper.optionalDependencies || {}).sort();
  const expected = Object.values(PLATFORMS)
    .map((entry) => entry.package)
    .sort();
  assert.deepEqual(declared, expected);
});

test("no unknown directory exists under npm/packages", () => {
  const dirs = fs.readdirSync(path.join(NPM_ROOT, "packages")).sort();
  assert.deepEqual(dirs, Object.keys(PLATFORMS).sort());
});
