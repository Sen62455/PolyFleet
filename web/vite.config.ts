import { fileURLToPath, URL } from "node:url";
import vue from "@vitejs/plugin-vue";
import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const proxyTarget = env.VITE_DEV_PROXY_TARGET || "http://127.0.0.1:8080";
  const proxyOrigin = env.VITE_DEV_PUBLIC_ORIGIN?.trim();
  const proxy = {
    target: proxyTarget,
    changeOrigin: true,
    ...(proxyOrigin ? { headers: { Origin: proxyOrigin } } : {}),
  };

  return {
    plugins: [vue()],
    resolve: {
      alias: {
        "@": fileURLToPath(new URL("./src", import.meta.url)),
      },
    },
    server: {
      host: "127.0.0.1",
      port: 5173,
      proxy: {
        "/api": proxy,
        "/agent": proxy,
        "/healthz": proxy,
      },
    },
    build: {
      outDir: "../internal/webui/dist",
      emptyOutDir: true,
      sourcemap: false,
    },
    test: {
      environment: "happy-dom",
    },
  };
});
