import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

export default defineConfig({
  plugins: [viteSingleFile()],
  build: {
    outDir: "dist",
    // The embed bundle is large and already minified; one file is the whole app.
    chunkSizeWarningLimit: 6000,
    rollupOptions: { input: process.env.INPUT },
  },
});
