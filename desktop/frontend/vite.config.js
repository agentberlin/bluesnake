import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

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
  build: {
    outDir: "dist",
    chunkSizeWarningLimit: 1500,
  },
});
