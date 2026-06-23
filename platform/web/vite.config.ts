/// <reference types="node" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/v1": {
        target: process.env.PLATFORM_API ?? "http://localhost:8787",
        changeOrigin: true,
      },
      "/healthz": process.env.PLATFORM_API ?? "http://localhost:8787",
    },
  },
});
