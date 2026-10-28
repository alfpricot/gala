#!/usr/bin/env node
// Launcher for the prebuilt gala binary. Each supported platform's binary is
// its own package, listed in optionalDependencies with matching os/cpu fields,
// so npm installs only the one for the machine; this finds it and hands it the
// arguments, stdio, and exit status unchanged.
"use strict";

const path = require("node:path");
const { spawnSync } = require("node:child_process");

const PLATFORMS = require(path.join(__dirname, "..", "platforms.json"));

function resolvePlatformPackage() {
  const { platform, arch } = process;
  const entry = Object.values(PLATFORMS).find(
    (p) => p.platform === platform && p.arch === arch,
  );
  if (!entry) {
    const supported = Object.values(PLATFORMS)
      .map((p) => `${p.platform}/${p.arch}`)
      .join(", ");
    throw new Error(
      `gala has no prebuilt binary for ${platform}/${arch} (${supported} only); ` +
        "install with `go install github.com/alfpricot/gala/cmd/gala@latest` instead",
    );
  }
  return { pkg: entry.package, binName: entry.binName };
}

function resolveBinPath() {
  const { pkg, binName } = resolvePlatformPackage();
  try {
    return require.resolve(`${pkg}/bin/${binName}`);
  } catch (err) {
    throw new Error(
      `gala: could not find the ${pkg} optional dependency (${err.message}). ` +
        "Likely cause: installed with --omit=optional, or the registry token " +
        `cannot read ${pkg} (each platform package needs its own access grant). ` +
        "Reinstall without --omit=optional, with read access to that package.",
    );
  }
}

function main() {
  let binPath;
  try {
    binPath = resolveBinPath();
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }

  const result = spawnSync(binPath, process.argv.slice(2), { stdio: "inherit" });
  if (result.error) {
    console.error(`gala: failed to run the native binary: ${result.error.message}`);
    process.exit(1);
  }
  process.exit(result.status ?? 1);
}

if (require.main === module) {
  main();
}

module.exports = { resolvePlatformPackage, PLATFORMS };
