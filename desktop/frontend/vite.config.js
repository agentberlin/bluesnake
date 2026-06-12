import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

/* single source of truth for the app version: internal/version/VERSION, the
   same file the Go binaries embed. Bump the version there only. */
const appVersion = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "..", "..", "internal", "version", "VERSION"),
  "utf8",
).trim();

/* dist/.gitkeep is committed so go:embed compiles on a fresh clone, but the
   build empties dist/ — recreate it after every build so it never shows up
   as a deletion in git. */
function keepGitkeep() {
  return {
    name: "keep-gitkeep",
    closeBundle() {
      writeFileSync(join(dirname(fileURLToPath(import.meta.url)), "dist", ".gitkeep"), "");
    },
  };
}

export default defineConfig({
  plugins: [react(), keepGitkeep()],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
  build: {
    outDir: "dist",
    chunkSizeWarningLimit: 1500,
  },
});
