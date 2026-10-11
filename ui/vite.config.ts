import { mkdirSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";
import solid from "vite-plugin-solid";

// Where the build goes: web/dist, which the Go package web embeds. The
// Nix derivation of the page sets GREAT_MACHINE_UI_DIST, as it may not
// write beside its source.
const dist = resolve(import.meta.dirname, process.env.GREAT_MACHINE_UI_DIST || "../web/dist");

// A build empties its directory first (cleanDist, below). So the
// directory may not be the one of this file, nor one that contains it:
// "." or ".." would have a build delete the sources of the page. The
// way from the directory of the build to this one says which it is: it
// is empty for the same directory, and leads only downwards from one
// that contains this one. (Any other way begins by going up, or, on a
// system with drives, is no relative one.)
const fromDist = relative(dist, import.meta.dirname);
if (!(fromDist === ".." || fromDist.startsWith(".." + sep) || isAbsolute(fromDist))) {
  throw new Error(
    `GREAT_MACHINE_UI_DIST is ${JSON.stringify(process.env.GREAT_MACHINE_UI_DIST)}, which is ${dist}: ` +
      `that is the directory of the page's sources (${import.meta.dirname}) or one that contains it, ` +
      `and a build empties its directory first. Name a directory of its own, such as dist.`,
  );
}

// The placeholder that git tracks in web/dist, without which go:embed
// would have no directory to embed before the first build.
const placeholder = ".gitkeep";

// cleanDist empties the directory of the build before a build, but for
// the placeholder. Vite's own emptyOutDir would delete the placeholder
// too, and a build that then failed would leave the Go module unable to
// compile.
function cleanDist(): Plugin {
  return {
    name: "great-machine:clean-dist",
    apply: "build",
    buildStart() {
      mkdirSync(dist, { recursive: true });
      for (const name of readdirSync(dist)) {
        if (name !== placeholder) {
          rmSync(join(dist, name), { recursive: true, force: true });
        }
      }
      writeFileSync(join(dist, placeholder), "");
    },
  };
}

export default defineConfig({
  plugins: [solid(), cleanDist()],
  build: {
    outDir: dist,
    // cleanDist does it, and keeps the placeholder.
    emptyOutDir: false,
    assetsDir: "assets",
    // No file is put into another as a data: URI: the content security
    // policy of the page allows its own origin alone, and data: is none.
    assetsInlineLimit: 0,
    sourcemap: false,
    target: "es2022",
  },
  // The unit tests are of what has no part in the document: they run in
  // Node as it is. (vite-plugin-solid would ask for jsdom otherwise.)
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
  server: {
    // draal, or `go run ./web/demo`, on its default address. A request is
    // passed on with the Host it came with (no changeOrigin): the API
    // refuses a WebSocket whose Origin is not its Host, and the Origin a
    // browser sends is this server.
    // The page of the store is draal's to serve as well.
    proxy: {
      "/api": { target: "http://127.0.0.1:8080", ws: true },
      "/store": { target: "http://127.0.0.1:8080" },
    },
  },
});
