// Loads .env from the PROJECT directory (not the cwd). Imported first in
// server.ts so Grafana credentials are available even when Claude Desktop
// launches the server from a different working directory. Keep this as the very
// first import so it runs before ./data.js reads process.env.
import dotenv from "dotenv";
import path from "node:path";

dotenv.config({ path: path.join(import.meta.dirname, "..", ".env") });
