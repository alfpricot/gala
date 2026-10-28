#!/bin/sh
# Stage the gala npm packages for one released version.
#
#   npm/build.sh <version> <outdir>
#
# <version> is the release tag without the "v" (1.2.3). Copies npm/ to
# <outdir>, cross-compiles gala into <outdir>/packages/<key>/bin/ for every
# entry in npm/platforms.json, and stamps <version> into every package.json,
# including the wrapper's optionalDependencies. <outdir> must not exist yet;
# publish each <outdir>/packages/<key> first, then <outdir> itself. Run from
# the repository root.

set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: npm/build.sh <version> <outdir>" >&2
    exit 2
fi
version="$1"
out="$2"

if [ -e "$out" ]; then
    echo "npm/build.sh: $out already exists" >&2
    exit 1
fi

cp -R npm "$out"
rm -rf "$out/test" "$out/build.sh"
cp README.md LICENSE "$out/"

# One "<key> <goos> <goarch> <binName>" line per platform.
node -e '
  const platforms = require("./npm/platforms.json");
  for (const [key, p] of Object.entries(platforms)) {
    console.log(`${key} ${p.goos} ${p.goarch} ${p.binName}`);
  }
' | while read -r key goos goarch bin; do
    echo "building $key"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
        -ldflags "-s -w -X main.galaVersion=v$version" \
        -o "$out/packages/$key/bin/$bin" ./cmd/gala
    chmod +x "$out/packages/$key/bin/$bin"
    cp LICENSE "$out/packages/$key/"
    (cd "$out/packages/$key" && npm pkg set "version=$version")
done

(cd "$out" && node -e '
  const fs = require("node:fs");
  const pkg = JSON.parse(fs.readFileSync("package.json", "utf8"));
  pkg.version = process.argv[1];
  for (const dep of Object.keys(pkg.optionalDependencies)) {
    pkg.optionalDependencies[dep] = pkg.version;
  }
  fs.writeFileSync("package.json", JSON.stringify(pkg, null, 2) + "\n");
' "$version")
