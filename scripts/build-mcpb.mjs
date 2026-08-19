#!/usr/bin/env node
/**
 * Builds the MCP Bundle (.mcpb) that Claude Desktop installs.
 *
 * Reads the binaries GoReleaser produced (from `dist/artifacts.json`), stages
 * them into the layout the manifest expects, generates the manifest's `tools`
 * array by asking the server itself, and packs the result with the `mcpb` CLI.
 *
 * The darwin binary is a universal Mach-O carrying both architectures, because
 * MCPB manifests can only vary the server command per OS, not per CPU
 * architecture (https://github.com/modelcontextprotocol/mcpb/issues/10). Linux
 * ships both binaries behind `launch.sh`, which picks one at start-up.
 *
 * Usage: node scripts/build-mcpb.mjs [--dist dist] [--out dist] [--version X.Y.Z]
 */

import { spawn } from "node:child_process";
import { copyFileSync, chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const repoRoot = fileURLToPath(new URL("..", import.meta.url));

function parseArgs(argv) {
  const args = { dist: "dist", out: "dist", version: process.env.MCPB_VERSION ?? null };
  for (let i = 0; i < argv.length; i += 2) {
    const key = argv[i]?.replace(/^--/, "");
    if (!(key in args)) {
      throw new Error(`unknown argument: ${argv[i]}`);
    }
    args[key] = argv[i + 1];
  }
  return args;
}

/**
 * Locates one built binary in GoReleaser's artifact metadata.
 *
 * Matching on metadata rather than on directory names keeps this working when
 * GoReleaser changes its `dist/` layout, as it has before.
 */
function findBinary(artifacts, { goos, goarch, id }) {
  const match = artifacts.find(
    (a) => a.type === "Binary" && a.goos === goos && a.goarch === goarch && a.extra?.ID === id,
  );
  if (!match) {
    throw new Error(`no binary in dist/artifacts.json for ${JSON.stringify({ goos, goarch, id })}`);
  }
  return match.path;
}

/** Copies a binary into the staging tree, executable. */
function stageBinary(source, destination) {
  mkdirSync(path.dirname(destination), { recursive: true });
  copyFileSync(source, destination);
  chmodSync(destination, 0o755);
}

/**
 * Asks a freshly built server for its tools over stdio.
 *
 * Generating this list means the bundle can never advertise tools the server no
 * longer has, which is how the previously published bundle came to list tools
 * that had been removed several releases earlier.
 */
async function collectTools(binary) {
  const child = spawn(binary, ["-t", "stdio"], { stdio: ["pipe", "pipe", "pipe"] });

  let settled = false;
  let stderr = "";
  child.stderr.on("data", (chunk) => {
    stderr += chunk;
  });
  // The child is killed as soon as it has answered, so a write can lose its pipe.
  child.stdin.on("error", () => {});

  const send = (message) => {
    if (!settled && child.stdin.writable) {
      child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", ...message })}\n`);
    }
  };

  const tools = await new Promise((resolve, reject) => {
    const finish = (fn, value) => {
      if (settled) return;
      settled = true;
      fn(value);
    };
    const fail = (message) =>
      finish(reject, new Error(`${message}${stderr ? `\nserver stderr: ${stderr.trim()}` : ""}`));

    child.on("error", (error) => fail(`could not run ${binary}: ${error.message}`));
    child.on("exit", (code) => fail(`server exited (code ${code}) before listing tools`));

    // Responses are newline-delimited JSON, and a chunk can split a line, so
    // hold the trailing partial line back until the rest of it arrives.
    let pending = "";
    child.stdout.on("data", (chunk) => {
      pending += chunk;
      const lines = pending.split("\n");
      pending = lines.pop() ?? "";

      for (const line of lines) {
        if (!line.trim()) continue;
        let message;
        try {
          message = JSON.parse(line);
        } catch (error) {
          fail(`could not parse server output (${error.message}): ${line.slice(0, 200)}`);
          return;
        }
        if (message.error) {
          fail(`server returned an error: ${JSON.stringify(message.error)}`);
          return;
        }
        if (message.id === 1) {
          send({ method: "notifications/initialized" });
          send({ id: 2, method: "tools/list", params: {} });
        }
        if (message.id === 2) {
          finish(resolve, message.result?.tools ?? []);
          return;
        }
      }
    });

    send({
      id: 1,
      method: "initialize",
      params: {
        protocolVersion: "2025-06-18",
        capabilities: {},
        clientInfo: { name: "build-mcpb", version: "1" },
      },
    });
  }).finally(() => child.kill());

  if (tools.length === 0) {
    throw new Error("server reported no tools");
  }

  return tools
    .map((tool) => {
      // Keep the manifest's tool descriptions to one line: the host shows them
      // in a list, and our full descriptions run to paragraphs.
      const description = (tool.description ?? "").split("\n")[0].trim();
      // `tools` entries accept only `name` and `description`: every schema
      // version sets additionalProperties:false, so read-only/destructive hints
      // cannot be declared here. They travel with the server's own tool
      // annotations over MCP instead.
      const entry = { name: tool.name };
      if (description) entry.description = description;
      return entry;
    })
    .sort((a, b) => a.name.localeCompare(b.name));
}

function run(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { stdio: "inherit", ...options });
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? resolve() : reject(new Error(`${command} ${args.join(" ")} exited with ${code}`)),
    );
  });
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const dist = path.resolve(repoRoot, args.dist);
  const artifacts = JSON.parse(readFileSync(path.join(dist, "artifacts.json"), "utf8"));

  const staging = path.join(dist, "mcpb-bundle");
  rmSync(staging, { recursive: true, force: true });
  mkdirSync(staging, { recursive: true });

  // One universal binary for macOS, both binaries plus a shim for Linux, amd64
  // for Windows (Windows on ARM emulates it).
  const layout = [
    // GoReleaser reports the merged universal binary as goarch "all".
    { selector: { goos: "darwin", goarch: "all", id: "mcpb-darwin-universal" }, dest: "server/darwin/mcp-grafana" },
    { selector: { goos: "linux", goarch: "amd64", id: "mcpb" }, dest: "server/linux-x64/mcp-grafana" },
    { selector: { goos: "linux", goarch: "arm64", id: "mcpb" }, dest: "server/linux-arm64/mcp-grafana" },
    { selector: { goos: "windows", goarch: "amd64", id: "mcpb" }, dest: "server/win32-x64/mcp-grafana.exe" },
  ];
  for (const { selector, dest } of layout) {
    const source = findBinary(artifacts, selector);
    stageBinary(source, path.join(staging, dest));
    console.error(`staged ${dest}`);
  }

  stageBinary(path.join(repoRoot, "mcpb", "launch.sh"), path.join(staging, "server/linux/launch.sh"));
  copyFileSync(path.join(repoRoot, "mcpb", "icon.png"), path.join(staging, "icon.png"));

  // Ask the Linux amd64 build (this script runs on CI's amd64 Linux) for the
  // tool list; every build in the bundle is the same code.
  const tools = await collectTools(path.join(staging, "server/linux-x64/mcp-grafana"));
  console.error(`generated ${tools.length} tool entries`);

  const manifest = JSON.parse(readFileSync(path.join(repoRoot, "mcpb", "manifest.template.json"), "utf8"));
  const metadata = JSON.parse(readFileSync(path.join(dist, "metadata.json"), "utf8"));
  // `tag` is the release being built; `version` carries a snapshot suffix for
  // local builds, where there is no tag.
  const version = (args.version ?? metadata.tag ?? metadata.version).replace(/^v/, "");
  manifest.version = version;
  manifest.tools = tools;
  writeFileSync(path.join(staging, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);

  const bundle = path.resolve(repoRoot, args.out, `mcp-grafana_${version}.mcpb`);
  // Pinned so the same commit always packs with the same toolchain; Renovate
  // keeps it current. Bundles are unsigned: `mcpb sign` needs a code-signing
  // certificate we do not have.
  const mcpb = "@anthropic-ai/mcpb@2.1.2";
  await run("npx", ["--yes", mcpb, "validate", path.join(staging, "manifest.json")]);
  await run("npx", ["--yes", mcpb, "pack", staging, bundle]);
  console.error(`\nbuilt ${path.relative(repoRoot, bundle)}`);
}

main().catch((error) => {
  console.error(`build-mcpb: ${error.message}`);
  process.exit(1);
});
