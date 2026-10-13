import type { Runtime } from "./types";

/** Runtimes that run as a process under systemd behind the proxy. */
export const appRuntimes: Runtime[] = ["node", "python", "java", "go", "dart"];
export const isApp = (r: Runtime) => appRuntimes.includes(r);
/** Nginx forwards these to a local port. */
export const isProxied = (r: Runtime) => isApp(r) || r === "proxy" || r === "docker";
/** Runtimes with a version to pick. */
export const versioned = (r: Runtime): r is "php" | "node" | "python" | "java" | "go" | "dart" => r === "php" || isApp(r);

export const defaultStart: Partial<Record<Runtime, string>> = {
  node: "npm start",
  python: "gunicorn app:app --bind 127.0.0.1:$PORT",
  java: "java -jar app.jar",
  go: "./app",
  dart: "dart run bin/server.dart",
};
