import { defineConfig } from "vite";
import wails from "@wailsio/runtime/plugins/vite";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));

// 应用图标的唯一来源。改这一张图，标题栏 / favicon / 各平台安装包图标同步生效。
// 注意：不要改名为 wails.png —— 那是 Wails 模板遗留路径，本项目的 public/ 下并不存在该文件。
const APPICON_SRC = path.resolve(root, "../build/appicon.png");

/**
 * 把 build/appicon.png 以 /appicon.png 暴露给前端：
 * - dev  ：vite dev server 直接读源文件，改图刷新即生效
 * - build：作为 asset emit 到 dist/
 */
function appicon() {
  return {
    name: "appicon",
    configureServer(server) {
      server.middlewares.use("/appicon.png", (_req, res) => {
        if (!fs.existsSync(APPICON_SRC)) {
          res.statusCode = 404;
          res.end("appicon.png not found at " + APPICON_SRC);
          return;
        }
        res.setHeader("Content-Type", "image/png");
        res.setHeader("Cache-Control", "no-cache");
        fs.createReadStream(APPICON_SRC).pipe(res);
      });
    },
    generateBundle() {
      if (!fs.existsSync(APPICON_SRC)) {
        this.warn(`appicon.png not found at ${APPICON_SRC}`);
        return;
      }
      this.emitFile({
        type: "asset",
        fileName: "appicon.png",
        source: fs.readFileSync(APPICON_SRC),
      });
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [wails("./bindings"), appicon()],
  build: {
    rollupOptions: {
      input: {
        main: "index.html",
      },
    },
  },
});
